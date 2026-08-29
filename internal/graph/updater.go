package graph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/graph/codegraph"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

var legalArticleRefRe = regexp.MustCompile(`(?i)^статья\s+(\d+(?:\.\d+)*)`)

var legalCodeNonAlnumRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

const (
	provenanceExtraction = "extraction"
	provenanceGoCode     = "go-code"
)

// GraphUpdater keeps the graph store in sync with a document's chunks:
// extraction, incremental removal of stale contributions, and scoped
// community/summary recomputation.
type GraphUpdater struct {
	Store          graphstore.Store
	Extractor      *Extractor
	LegalExtractor *LegalExtractor
	ChatExtractor  *ChatExtractor
	Summarizer     *Summarizer
	Community      CommunityDetector
	Seed           int64
	CodeRoot       string

	bulk               bool
	bulkTouched        map[string]struct{}
	extractConcurrency int
}

// BeginBulk defers per-document community recomputation to EndBulk: without
// it, a multi-document reindex reloads and re-analyzes the entire graph
// (componentIndex: AllEntities + AllRelations) once per document, which is
// O(n^2) as the graph grows over the run. Callers doing a bulk walk (e.g.
// Indexer.buildAll) should pair this with a deferred EndBulk.
func (u *GraphUpdater) BeginBulk() {
	if u == nil {
		return
	}
	u.bulk = true
	u.bulkTouched = map[string]struct{}{}
}

// EndBulk recomputes communities once for every entity touched since
// BeginBulk (a single componentIndex pass) and turns bulk mode off.
func (u *GraphUpdater) EndBulk(ctx context.Context) error {
	if u == nil {
		return nil
	}
	u.bulk = false
	touched := u.bulkTouched
	u.bulkTouched = nil
	if len(touched) == 0 {
		return nil
	}
	_, err := u.recomputeCommunities(ctx, touched)
	return err
}

func NewGraphUpdater(store graphstore.Store, extractor *Extractor, summarizer *Summarizer) *GraphUpdater {
	return &GraphUpdater{Store: store, Extractor: extractor, Summarizer: summarizer, Community: LouvainDetector{}, Seed: 1}
}

// WithExtractConcurrency bounds how many per-chunk LLM extractions of one
// document may run in parallel. The default (1) keeps calls strictly
// sequential; raise it to the LLM server's num_parallel to shorten bulk
// index runs.
func (u *GraphUpdater) WithExtractConcurrency(n int) *GraphUpdater {
	if u != nil && n > 1 {
		u.extractConcurrency = n
	}
	return u
}

func (u *GraphUpdater) WithCodeRoot(root string) *GraphUpdater {
	u.CodeRoot = root
	return u
}

func (u *GraphUpdater) WithCommunityDetector(d CommunityDetector) *GraphUpdater {
	if u != nil && d != nil {
		u.Community = d
	}
	return u
}

// WithLegalExtractor enables domain-specific extraction for legal documents
// (legal-article/legal-plenum kind chunks): the LegalExtractor drives the
// LLM prompts, and the deterministic AMENDS contribution is built from the
// article's redaction metadata even when no LLM is available.
func (u *GraphUpdater) WithLegalExtractor(e *LegalExtractor) *GraphUpdater {
	if u != nil {
		u.LegalExtractor = e
	}
	return u
}

// WithChatExtractor enables the chat-specific extraction path: chunks of
// chat documents (kind "message", or chunks carrying ChatChunker thread
// metadata) build a thread-scope mini-graph instead of going through the
// generic per-chunk extractor. When unset, chat chunks fall back to the
// generic extractor (fail-open).
func (u *GraphUpdater) WithChatExtractor(e *ChatExtractor) *GraphUpdater {
	if u != nil {
		u.ChatExtractor = e
	}
	return u
}

// UpdateDocument re-extracts the graph contribution of a document's chunks:
// it first strips any existing entity/relation references to this
// document's chunk ids (oldChunkIDs from a previous index, plus the new
// ids, so a document that shrank does not leave stale references to its
// dropped tail chunks), merges in fresh extraction from the given chunks,
// prunes anything left with no supporting chunks, and finally recomputes
// communities/summaries only for the connected components touched by the
// change. The returned entity ids are only those the document's new chunks
// reference (not entities it dropped), so blast-radius invalidation never
// marks other documents from knowledge this version no longer carries.
func (u *GraphUpdater) UpdateDocument(ctx context.Context, docID string, chunks []vector.Chunk, oldChunkIDs ...string) ([]string, error) {
	if u == nil || u.Store == nil {
		return nil, fmt.Errorf("graph: UpdateDocument: nil updater or store")
	}

	chunkIDs := make([]string, 0, len(chunks))
	for _, c := range chunks {
		chunkIDs = append(chunkIDs, c.ID)
	}
	chunkIDs = append(chunkIDs, oldChunkIDs...)

	removedTouched, err := u.Store.RemoveChunks(ctx, chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("graph: UpdateDocument(%q): remove chunks: %w", docID, err)
	}

	touched := make(map[string]struct{}, len(removedTouched))
	for _, id := range removedTouched {
		touched[id] = struct{}{}
	}
	newTouched := make(map[string]struct{})

	var newEntities []graphstore.Entity
	var newRelations []graphstore.Relation

	if len(chunks) > 0 && isCodeDocument(chunks) {
		files := []codegraph.File{{Path: codeSourcePath(chunks[0]), Src: []byte(chunks[0].Text)}}
		if u.CodeRoot != "" {
			files = u.codePackageFiles(chunks[0], files)
		}
		codeEntities, codeRelations, err := codegraph.ExtractFiles(files)
		if err != nil {
			return nil, fmt.Errorf("graph: UpdateDocument(%q): code extraction: %w", docID, err)
		}
		chunkID := chunks[0].ID
		for i := range codeEntities {
			codeEntities[i].SourceChunks = []string{chunkID}
			touched[codeEntities[i].ID] = struct{}{}
		}
		for i := range codeRelations {
			codeRelations[i].SourceChunks = []string{chunkID}
			codeRelations[i].Confidence = 1.0
			codeRelations[i].Provenance = provenanceGoCode
			touched[codeRelations[i].Src] = struct{}{}
			touched[codeRelations[i].Dst] = struct{}{}
		}
		newEntities = append(newEntities, codeEntities...)
		newRelations = append(newRelations, codeRelations...)
	} else if len(chunks) > 0 && u.ChatExtractor != nil && isChatDocument(chunks) {
		// Chat documents (kind "message", or chunks carrying ChatChunker
		// thread metadata) build a thread-scope mini-graph through the
		// ChatExtractor: speaker entities, small-talk filtering, and
		// DECIDED/PROPOSED/AGREED edges stamped with message timestamps.
		// The mini-graph merges into the common graph through the same
		// UpsertEntities/UpsertRelations path as generic extraction.
		chatEntities, chatRelations, err := u.ChatExtractor.ExtractThread(ctx, chunks)
		if err != nil {
			return nil, fmt.Errorf("graph: UpdateDocument(%q): chat extraction: %w", docID, err)
		}
		chatUserIDs := make(map[string]struct{})
		for i := range chatEntities {
			touched[chatEntities[i].ID] = struct{}{}
			if chatEntities[i].Type == chatUserEntityType {
				chatUserIDs[chatEntities[i].ID] = struct{}{}
				continue
			}
			newTouched[chatEntities[i].ID] = struct{}{}
		}
		for i := range chatRelations {
			chatRelations[i].Provenance = provenanceExtraction
			if u.ChatExtractor != nil {
				chatRelations[i].ExtractorVersion = u.ChatExtractor.Model
			}
			touched[chatRelations[i].Src] = struct{}{}
			touched[chatRelations[i].Dst] = struct{}{}
			if _, ok := chatUserIDs[chatRelations[i].Src]; !ok {
				newTouched[chatRelations[i].Src] = struct{}{}
			}
			if _, ok := chatUserIDs[chatRelations[i].Dst]; !ok {
				newTouched[chatRelations[i].Dst] = struct{}{}
			}
		}
		newEntities = append(newEntities, chatEntities...)
		newRelations = append(newRelations, chatRelations...)
	} else {
		// Legal articles get a deterministic anchor contribution (article
		// entity, per-amendment Action entities, AMENDS edges with bi-temporal
		// validity) built once per document from the first chunk's frontmatter
		// metadata — no LLM involved.
		var legalEntities []graphstore.Entity
		var legalRelations []graphstore.Relation
		if len(chunks) > 0 {
			if ents, rels, ok := BuildLegalArticleContribution(chunks[0].Metadata); ok {
				legalEntities, legalRelations = ents, rels
			}
		}

		var articleAnchors []graphstore.Entity
		for _, c := range chunks {
			if c.Metadata["kind"] != KindLegalArticle {
				continue
			}
			article, ok := BuildLegalArticleEntity(c.Metadata)
			if !ok {
				continue
			}
			article.SourceChunks = []string{c.ID}
			newEntities = append(newEntities, article)
			touched[article.ID] = struct{}{}
			newTouched[article.ID] = struct{}{}
			articleAnchors = append(articleAnchors, article)
		}

		extractions := u.extractChunks(ctx, chunks)

		for i, c := range chunks {
			extraction := extractions[i]

			entities, nameToID := BuildEntities(extraction.Entities)
			relations := BuildRelations(nameToID, extraction.Relations)
			for i := range relations {
				relations[i].Provenance = provenanceExtraction
				if u.Extractor != nil {
					relations[i].ExtractorVersion = u.Extractor.Model
				}
			}
			if c.Metadata["kind"] == KindLegalPlenum {
				entities, relations = u.canonicalizePlenumContribution(ctx, c, entities, relations)
			}
			for i := range entities {
				entities[i].SourceChunks = []string{c.ID}
				touched[entities[i].ID] = struct{}{}
				newTouched[entities[i].ID] = struct{}{}
			}
			for i := range relations {
				relations[i].SourceChunks = []string{c.ID}
				touched[relations[i].Src] = struct{}{}
				touched[relations[i].Dst] = struct{}{}
				newTouched[relations[i].Src] = struct{}{}
				newTouched[relations[i].Dst] = struct{}{}
			}
			newEntities = append(newEntities, entities...)
			newRelations = append(newRelations, relations...)
		}

		if len(legalEntities) > 0 {
			chunkID := chunks[0].ID
			for i := range legalEntities {
				legalEntities[i].SourceChunks = []string{chunkID}
				touched[legalEntities[i].ID] = struct{}{}
				newTouched[legalEntities[i].ID] = struct{}{}
			}
			for i := range legalRelations {
				legalRelations[i].SourceChunks = []string{chunkID}
				touched[legalRelations[i].Src] = struct{}{}
				touched[legalRelations[i].Dst] = struct{}{}
				newTouched[legalRelations[i].Src] = struct{}{}
				newTouched[legalRelations[i].Dst] = struct{}{}
			}
			newEntities = append(newEntities, legalEntities...)
			newRelations = append(newRelations, legalRelations...)
		}

		if len(articleAnchors) > 0 {
			extraEntities, err := u.retargetPlenumInterprets(ctx, articleAnchors, touched)
			if err != nil {
				return nil, fmt.Errorf("graph: UpdateDocument(%q): retarget plenum interprets: %w", docID, err)
			}
			newEntities = append(newEntities, extraEntities...)
			for _, e := range extraEntities {
				touched[e.ID] = struct{}{}
				newTouched[e.ID] = struct{}{}
			}
		}
	}

	if err := u.Store.UpsertEntities(ctx, newEntities); err != nil {
		return nil, fmt.Errorf("graph: UpdateDocument(%q): upsert entities: %w", docID, err)
	}
	if err := u.Store.UpsertRelations(ctx, newRelations); err != nil {
		return nil, fmt.Errorf("graph: UpdateDocument(%q): upsert relations: %w", docID, err)
	}
	if err := u.Store.PruneOrphans(ctx); err != nil {
		return nil, fmt.Errorf("graph: UpdateDocument(%q): prune orphans: %w", docID, err)
	}

	if len(touched) == 0 {
		return nil, nil
	}
	if u.bulk {
		for id := range touched {
			u.bulkTouched[id] = struct{}{}
		}
	} else if _, err := u.recomputeCommunities(ctx, touched); err != nil {
		return nil, err
	}
	live := make([]string, 0, len(newTouched))
	for id := range newTouched {
		live = append(live, id)
	}
	sort.Strings(live)
	return live, nil
}

func isCodeDocument(chunks []vector.Chunk) bool {
	for _, c := range chunks {
		if c.Metadata["kind"] == codegraph.KindCode {
			return true
		}
		// .go files fetched through other connectors (e.g. GitHub content)
		// carry a different kind but must still take the deterministic
		// code-graph extraction path, not generic LLM extraction.
		if strings.HasSuffix(strings.ToLower(c.FileName), ".go") {
			return true
		}
		if strings.HasSuffix(strings.ToLower(c.Metadata["path"]), ".go") {
			return true
		}
		if strings.HasSuffix(strings.ToLower(c.Metadata["id"]), ".go") {
			return true
		}
	}
	return false
}

// codeSourcePath prefers the document's original file path (carried into
// chunk metadata by the indexer) over the flattened rendered path, so the
// code-graph extractor qualifies symbols by their real package directory
// and packages in the same source never collapse into one node.
func codeSourcePath(c vector.Chunk) string {
	if raw := c.Metadata["id"]; raw != "" && strings.HasSuffix(strings.ToLower(raw), ".go") {
		return raw
	}
	return c.FilePath
}

var packageClauseRe = regexp.MustCompile(`(?m)^package\s+([A-Za-z_]\w*)`)

func packageClause(src []byte) string {
	m := packageClauseRe.FindSubmatch(src)
	if len(m) != 2 {
		return ""
	}
	return string(m[1])
}

func (u *GraphUpdater) codePackageFiles(c vector.Chunk, files []codegraph.File) []codegraph.File {
	pkg := packageClause(files[0].Src)
	if pkg == "" {
		return files
	}
	dir := filepath.Join(u.CodeRoot, filepath.Dir(c.FilePath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	seen := map[string]bool{files[0].Path: true}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(filepath.Dir(c.FilePath), e.Name()))
		if seen[rel] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		doc, err := render.Parse(data)
		if err != nil {
			continue
		}
		if !codeDocument(doc) || packageClause([]byte(doc.Body)) != pkg {
			continue
		}
		seen[rel] = true
		sibPath := rel
		if raw := doc.ID; raw != "" && strings.HasSuffix(strings.ToLower(raw), ".go") {
			sibPath = raw
		}
		files = append(files, codegraph.File{Path: sibPath, Src: []byte(doc.Body)})
	}
	return files
}

func codeDocument(doc connector.Document) bool {
	if doc.Kind == codegraph.KindCode {
		return true
	}
	if strings.HasSuffix(strings.ToLower(doc.ID), ".go") {
		return true
	}
	switch p := doc.Frontmatter["path"].(type) {
	case string:
		return strings.HasSuffix(strings.ToLower(p), ".go")
	case nil:
		return false
	default:
		return strings.HasSuffix(strings.ToLower(fmt.Sprint(p)), ".go")
	}
}

// isChatDocument reports whether a document's chunks come from a chat
// connector: the indexer carries the document kind ("message") into chunk
// metadata, and ChatChunker chunks additionally carry a "thread_id" key
// (possibly empty for standalone messages). The thread_id fallback keeps
// chat routing working for chunks indexed before kind propagation existed.
func isChatDocument(chunks []vector.Chunk) bool {
	for _, c := range chunks {
		if c.Metadata["kind"] == KindChatMessage {
			return true
		}
		if _, ok := c.Metadata["thread_id"]; ok {
			return true
		}
	}
	return false
}

// extractChunk routes the chunk to the domain-appropriate extractor based
// on its kind metadata. A chunk with no matching extractor yields a zero
// Extraction (fail-open).
func (u *GraphUpdater) extractChunk(ctx context.Context, c vector.Chunk) (Extraction, error) {
	switch c.Metadata["kind"] {
	case KindLegalArticle:
		if u.LegalExtractor != nil {
			return u.LegalExtractor.ExtractArticle(ctx, c.Text)
		}
	case KindLegalPlenum:
		if u.LegalExtractor != nil {
			return u.LegalExtractor.ExtractPlenum(ctx, c.Text)
		}
	default:
		if u.Extractor != nil {
			return u.Extractor.ExtractChunk(ctx, c.Text)
		}
	}
	return Extraction{}, nil
}

// extractChunks runs extractChunk for every chunk, in order, with up to
// extractConcurrency LLM calls in flight. Errors degrade to a zero
// Extraction (fail-open), matching the pre-parallelization behavior of
// skipping the chunk entirely. Results are indexed by chunk position so
// downstream processing stays deterministic regardless of completion order.
func (u *GraphUpdater) extractChunks(ctx context.Context, chunks []vector.Chunk) []Extraction {
	out := make([]Extraction, len(chunks))
	if u.extractConcurrency <= 1 || len(chunks) <= 1 {
		for i, c := range chunks {
			out[i], _ = u.extractChunk(ctx, c)
		}
		return out
	}
	workers := u.extractConcurrency
	if workers > len(chunks) {
		workers = len(chunks)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i], _ = u.extractChunk(ctx, chunks[i])
		}(i)
	}
	wg.Wait()
	return out
}

// RemoveDocument strips a document's graph contribution entirely: it takes
// the chunk ids the document previously had (the caller — typically an
// indexer deleting the document from the vector store — already knows
// these, since deletion elsewhere requires them too), removes those
// references, prunes anything left with no supporting chunks, and
// recomputes communities/summaries for the affected components. Unlike
// UpdateDocument it never re-extracts or re-adds.
func (u *GraphUpdater) RemoveDocument(ctx context.Context, docID string, chunkIDs []string) error {
	if u == nil || u.Store == nil {
		return fmt.Errorf("graph: RemoveDocument: nil updater or store")
	}

	touchedList, err := u.Store.RemoveChunks(ctx, chunkIDs)
	if err != nil {
		return fmt.Errorf("graph: RemoveDocument(%q): remove chunks: %w", docID, err)
	}
	if err := u.Store.PruneOrphans(ctx); err != nil {
		return fmt.Errorf("graph: RemoveDocument(%q): prune orphans: %w", docID, err)
	}

	if len(touchedList) == 0 {
		return nil
	}
	touched := make(map[string]struct{}, len(touchedList))
	for _, id := range touchedList {
		touched[id] = struct{}{}
	}
	if u.bulk {
		for id := range touched {
			u.bulkTouched[id] = struct{}{}
		}
		return nil
	}
	_, err = u.recomputeCommunities(ctx, touched)
	return err
}

// RefreshStaleCommunities recomputes every connected component that owns at
// least one stale community row: detection replaces the component's stored
// rows (fresh and stale alike) and clears the stale flag. Stale rows whose
// members no longer exist are deleted as orphans. Returns the number of
// stale communities refreshed; fail-open when there is nothing to do.
func (u *GraphUpdater) RefreshStaleCommunities(ctx context.Context) (int, error) {
	if u == nil || u.Store == nil {
		return 0, fmt.Errorf("graph: refresh stale communities: nil updater or store")
	}
	stale, err := u.Store.StaleCommunityCount(ctx)
	if err != nil {
		return 0, fmt.Errorf("graph: refresh stale communities: stale count: %w", err)
	}
	if stale == 0 {
		return 0, nil
	}

	entityByID, components, componentByEntity, allRelations, err := u.componentIndex(ctx)
	if err != nil {
		return 0, fmt.Errorf("graph: refresh stale communities: %w", err)
	}
	if len(entityByID) == 0 {
		all, err := u.Store.AllCommunities(ctx)
		if err != nil {
			return 0, fmt.Errorf("graph: refresh stale communities: all communities: %w", err)
		}
		ids := make([]string, 0, len(all))
		for _, c := range all {
			if c.Stale {
				ids = append(ids, c.ID)
			}
		}
		if len(ids) > 0 {
			if err := u.Store.DeleteCommunities(ctx, ids); err != nil {
				return 0, fmt.Errorf("graph: refresh stale communities: cleanup: %w", err)
			}
		}
		return 0, nil
	}

	staleCommunities, err := u.Store.AllCommunities(ctx)
	if err != nil {
		return 0, fmt.Errorf("graph: refresh stale communities: all communities: %w", err)
	}
	var staleRows []graphstore.Community
	for _, c := range staleCommunities {
		if c.Stale {
			staleRows = append(staleRows, c)
		}
	}
	if len(staleRows) == 0 {
		return 0, nil
	}

	refreshed := 0
	handled := make(map[string]struct{}, len(staleRows))
	affected := make(map[int]struct{})
	for _, c := range staleRows {
		for _, m := range c.Members {
			ci, ok := componentByEntity[m]
			if !ok {
				continue
			}
			affected[ci] = struct{}{}
			handled[c.ID] = struct{}{}
			break
		}
	}

	for ci := range affected {
		members := components[ci]
		memberSet := make(map[string]struct{}, len(members))
		for _, id := range members {
			memberSet[id] = struct{}{}
		}
		var toDelete []graphstore.Community
		for _, c := range staleCommunities {
			overlaps := false
			for _, m := range c.Members {
				if _, ok := memberSet[m]; ok {
					overlaps = true
					break
				}
			}
			if overlaps {
				toDelete = append(toDelete, c)
			}
		}
		oldByID := make(map[string]graphstore.Community, len(toDelete))
		for _, c := range toDelete {
			oldByID[c.ID] = c
		}
		newComms, err := u.detectComponent(ctx, members, entityByID, allRelations, oldByID)
		if err != nil {
			return 0, fmt.Errorf("graph: refresh stale communities: component: %w", err)
		}
		newIDs := make(map[string]struct{}, len(newComms))
		for _, c := range newComms {
			newIDs[c.ID] = struct{}{}
		}
		var leftovers []string
		for _, c := range toDelete {
			if c.Stale {
				refreshed++
			}
			if _, ok := newIDs[c.ID]; !ok {
				leftovers = append(leftovers, c.ID)
			}
		}
		if len(leftovers) > 0 {
			if err := u.Store.DeleteCommunities(ctx, leftovers); err != nil {
				return 0, fmt.Errorf("graph: refresh stale communities: delete leftovers: %w", err)
			}
		}
	}

	var orphanIDs []string
	for _, c := range staleRows {
		if _, ok := handled[c.ID]; ok {
			continue
		}
		orphanIDs = append(orphanIDs, c.ID)
	}
	if len(orphanIDs) > 0 {
		if err := u.Store.DeleteCommunities(ctx, orphanIDs); err != nil {
			return 0, fmt.Errorf("graph: refresh stale communities: delete orphans: %w", err)
		}
	}
	return refreshed, nil
}

func (u *GraphUpdater) recomputeCommunities(ctx context.Context, touched map[string]struct{}) ([]string, error) {
	entityByID, components, componentByEntity, _, err := u.componentIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("graph: recompute communities: %w", err)
	}

	liveTouched := make([]string, 0, len(touched))
	for id := range touched {
		if _, ok := entityByID[id]; ok {
			liveTouched = append(liveTouched, id)
		}
	}
	if len(liveTouched) == 0 {
		return nil, nil
	}

	affected := map[int]struct{}{}
	for _, id := range liveTouched {
		if ci, ok := componentByEntity[id]; ok {
			affected[ci] = struct{}{}
		}
	}

	for ci := range affected {
		if err := u.markComponentStale(ctx, components[ci], entityByID); err != nil {
			return nil, err
		}
	}
	return liveTouched, nil
}

// RecomputeCommunities is the manual graph-edit entry point: it marks every
// community overlapping entityIDs stale (including communities whose members
// were just deleted, since CommunitiesFor matches stored members rather than
// live entities), flags the connected components of still-live entities, and
// refreshes all stale communities in one pass. The returned ids are the live
// touched entities used for blast-radius bookkeeping by callers.
func (u *GraphUpdater) RecomputeCommunities(ctx context.Context, entityIDs []string) ([]string, error) {
	if u == nil || u.Store == nil {
		return nil, fmt.Errorf("graph: RecomputeCommunities: nil updater or store")
	}
	touched := make(map[string]struct{}, len(entityIDs))
	for _, id := range entityIDs {
		if id = strings.TrimSpace(id); id != "" {
			touched[id] = struct{}{}
		}
	}
	if len(touched) == 0 {
		return nil, nil
	}

	staleCommunities, err := u.Store.CommunitiesFor(ctx, entityIDs)
	if err != nil {
		return nil, fmt.Errorf("graph: RecomputeCommunities: communities for: %w", err)
	}
	staleIDs := make([]string, 0, len(staleCommunities))
	for _, c := range staleCommunities {
		staleIDs = append(staleIDs, c.ID)
	}
	if len(staleIDs) > 0 {
		if err := u.Store.MarkCommunitiesStale(ctx, staleIDs); err != nil {
			return nil, fmt.Errorf("graph: RecomputeCommunities: mark stale: %w", err)
		}
	}

	live, err := u.recomputeCommunities(ctx, touched)
	if err != nil {
		return nil, fmt.Errorf("graph: RecomputeCommunities: %w", err)
	}
	if _, err := u.RefreshStaleCommunities(ctx); err != nil {
		return nil, fmt.Errorf("graph: RecomputeCommunities: refresh: %w", err)
	}
	return live, nil
}

// componentIndex loads the full graph and derives the entity index plus
// connected components, shared by the write-path and stale-refresh paths.
func (u *GraphUpdater) componentIndex(ctx context.Context) (map[string]graphstore.Entity, [][]string, map[string]int, []graphstore.Relation, error) {
	allEntities, err := u.Store.AllEntities(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	allRelations, err := u.Store.AllRelations(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	entityByID := make(map[string]graphstore.Entity, len(allEntities))
	for _, e := range allEntities {
		entityByID[e.ID] = e
	}
	adjacency := buildAdjacency(allEntities, allRelations)
	components := connectedComponents(allEntities, adjacency)
	componentByEntity := make(map[string]int, len(allEntities))
	for ci, comp := range components {
		for _, id := range comp {
			componentByEntity[id] = ci
		}
	}
	return entityByID, components, componentByEntity, allRelations, nil
}

// markComponentStale implements the lazy-communities write path: existing
// community rows of a touched component are flagged stale and kept (their
// summaries still serve retrieval) until a batch refresh recomputes them.
// A component without any community rows yet (a brand-new document/component)
// is detected immediately so first-time communities appear right away.
func (u *GraphUpdater) markComponentStale(ctx context.Context, memberIDs []string, entityByID map[string]graphstore.Entity) error {
	if u == nil || u.Store == nil {
		return fmt.Errorf("graph: mark component stale: nil updater or store")
	}
	existing, err := u.Store.CommunitiesFor(ctx, memberIDs)
	if err != nil {
		return fmt.Errorf("graph: mark component stale: communities for: %w", err)
	}
	ids := make([]string, 0, len(existing))
	for _, c := range existing {
		ids = append(ids, c.ID)
	}
	if len(ids) == 0 {
		allRelations, err := u.Store.AllRelations(ctx)
		if err != nil {
			return fmt.Errorf("graph: mark component stale: all relations: %w", err)
		}
		_, err = u.detectComponent(ctx, memberIDs, entityByID, allRelations, nil)
		return err
	}
	if err := u.Store.MarkCommunitiesStale(ctx, ids); err != nil {
		return fmt.Errorf("graph: mark component stale: %w", err)
	}
	return nil
}

// detectComponent runs community detection for one connected component,
// upserts its fresh rows and returns them. Summaries of surviving community
// ids are reused; fresh ids get LLM summaries through the Summarizer.
func (u *GraphUpdater) detectComponent(ctx context.Context, memberIDs []string, entityByID map[string]graphstore.Entity, allRelations []graphstore.Relation, oldByID map[string]graphstore.Community) ([]graphstore.Community, error) {
	sort.Strings(memberIDs)

	memberEntities := make([]graphstore.Entity, 0, len(memberIDs))
	for _, id := range memberIDs {
		memberEntities = append(memberEntities, entityByID[id])
	}
	memberSet := make(map[string]struct{}, len(memberIDs))
	for _, id := range memberIDs {
		memberSet[id] = struct{}{}
	}
	var memberRelations []graphstore.Relation
	for _, r := range allRelations {
		_, sOK := memberSet[r.Src]
		_, dOK := memberSet[r.Dst]
		if sOK && dOK {
			memberRelations = append(memberRelations, r)
		}
	}

	newCommunities := u.Community.Detect(memberEntities, memberRelations, u.Seed)
	for i, c := range newCommunities {
		if prev, ok := oldByID[c.ID]; ok && prev.Summary != "" {
			newCommunities[i].Title = prev.Title
			newCommunities[i].Summary = prev.Summary
			continue
		}
		commEntities, commRelations := membersOf(c.Members, entityByID, memberRelations)
		if u.Summarizer != nil {
			title, summary := u.Summarizer.Summarize(ctx, commEntities, commRelations)
			newCommunities[i].Title = title
			newCommunities[i].Summary = summary
		}
	}

	if err := u.Store.UpsertCommunities(ctx, newCommunities); err != nil {
		return nil, fmt.Errorf("graph: recompute communities: upsert: %w", err)
	}
	return newCommunities, nil
}

func membersOf(ids []string, entityByID map[string]graphstore.Entity, relations []graphstore.Relation) ([]graphstore.Entity, []graphstore.Relation) {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	entities := make([]graphstore.Entity, 0, len(ids))
	for _, id := range ids {
		entities = append(entities, entityByID[id])
	}
	var rels []graphstore.Relation
	for _, r := range relations {
		_, sOK := set[r.Src]
		_, dOK := set[r.Dst]
		if sOK && dOK {
			rels = append(rels, r)
		}
	}
	return entities, rels
}

func buildAdjacency(entities []graphstore.Entity, relations []graphstore.Relation) map[string][]string {
	adj := make(map[string][]string, len(entities))
	for _, e := range entities {
		adj[e.ID] = nil
	}
	for _, r := range relations {
		adj[r.Src] = append(adj[r.Src], r.Dst)
		adj[r.Dst] = append(adj[r.Dst], r.Src)
	}
	return adj
}

func connectedComponents(entities []graphstore.Entity, adj map[string][]string) [][]string {
	visited := make(map[string]bool, len(entities))
	ids := make([]string, 0, len(entities))
	for _, e := range entities {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)

	var components [][]string
	for _, id := range ids {
		if visited[id] {
			continue
		}
		var comp []string
		queue := []string{id}
		visited[id] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			comp = append(comp, cur)
			for _, n := range adj[cur] {
				if !visited[n] {
					visited[n] = true
					queue = append(queue, n)
				}
			}
		}
		components = append(components, comp)
	}
	return components
}

func (u *GraphUpdater) canonicalizePlenumContribution(ctx context.Context, c vector.Chunk, entities []graphstore.Entity, relations []graphstore.Relation) ([]graphstore.Entity, []graphstore.Relation) {
	point, ok := BuildLegalPlenumPointEntity(c.Metadata)
	if !ok {
		return entities, relations
	}
	point.Description = ""
	pointIDs := map[string]struct{}{}
	for _, e := range entities {
		if e.Type != "пункт" {
			continue
		}
		pointIDs[e.ID] = struct{}{}
		if e.Description != "" {
			point.Description = e.Description
		}
	}
	if point.Description == "" {
		point.Description = "Пункт постановления Пленума ВС РФ " + c.Metadata["id"]
	}
	articleCanon := map[string]graphstore.Entity{}
	for _, e := range entities {
		if e.Type != "статья" {
			continue
		}
		if canon, ok := u.canonicalArticleAnchor(ctx, e.Name); ok {
			canon.Description = e.Description
			articleCanon[e.ID] = canon
		}
	}
	out := make([]graphstore.Entity, 0, len(entities)+1)
	for _, e := range entities {
		if _, drop := pointIDs[e.ID]; drop {
			continue
		}
		if canon, ok := articleCanon[e.ID]; ok {
			e.ID = canon.ID
			e.Name = canon.Name
			e.Type = KindLegalArticle
		}
		out = append(out, e)
	}
	for i := range relations {
		r := &relations[i]
		typ := strings.ToLower(strings.TrimSpace(r.Type))
		if typ == "interprets" || typ == "clarifies" {
			rewritten := false
			if _, drop := pointIDs[r.Src]; drop {
				r.Src = point.ID
				rewritten = true
			}
			if canon, ok := articleCanon[r.Dst]; ok {
				r.Dst = canon.ID
				rewritten = true
			}
			if rewritten {
				r.ID = RelationID(r.Src, r.Dst, r.Type)
			}
		}
	}
	return append(out, point), relations
}

func (u *GraphUpdater) canonicalArticleAnchor(ctx context.Context, name string) (graphstore.Entity, bool) {
	m := legalArticleRefRe.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return graphstore.Entity{}, false
	}
	articles, err := u.Store.AllEntities(ctx)
	if err != nil {
		return graphstore.Entity{}, false
	}
	num := m[1]
	var candidates []graphstore.Entity
	for _, e := range articles {
		if e.Type != KindLegalArticle {
			continue
		}
		em := legalArticleRefRe.FindStringSubmatch(normalizeName(e.Name))
		if em == nil || em[1] != num {
			continue
		}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		return graphstore.Entity{}, false
	}
	if code := codeNameFromRef(name); code != "" {
		codeKey := legalCodeKey(code)
		var narrowed []graphstore.Entity
		for _, e := range candidates {
			if strings.Contains(legalCodeKey(e.Name+" "+e.Description), codeKey) {
				narrowed = append(narrowed, e)
			}
		}
		if len(narrowed) > 0 {
			candidates = narrowed
		}
	}
	if len(candidates) != 1 {
		return graphstore.Entity{}, false
	}
	return candidates[0], true
}

// retargetPlenumInterprets repairs INTERPRETS/CLARIFIES edges that still
// point at a transient "статья" entity after the plenum was indexed before
// its article: canonicalizePlenumContribution only rewrites targets at
// plenum index time, when the anchor may not exist yet. Once this document
// creates the canonical legal-article anchor, the stored edges are
// re-pointed at it and the transient duplicates are removed. Returns the
// canonical anchors that gained the transient entities' source chunks (so
// the plenum chunk supports the canonical article, mirroring the
// plenum-time canonicalization), plus the touched ids.
func (u *GraphUpdater) retargetPlenumInterprets(ctx context.Context, articleAnchors []graphstore.Entity, touched map[string]struct{}) ([]graphstore.Entity, error) {
	if u == nil || u.Store == nil || len(articleAnchors) == 0 {
		return nil, nil
	}
	anchors := map[string][]graphstore.Entity{}
	for _, e := range articleAnchors {
		if e.Type != KindLegalArticle {
			continue
		}
		m := legalArticleRefRe.FindStringSubmatch(strings.TrimSpace(e.Name))
		if m == nil {
			continue
		}
		key := "статья " + m[1]
		anchors[key] = append(anchors[key], e)
	}
	if len(anchors) == 0 {
		return nil, nil
	}

	entities, err := u.Store.AllEntities(ctx)
	if err != nil {
		return nil, fmt.Errorf("list entities: %w", err)
	}
	transientByID := map[string]graphstore.Entity{}
	for _, e := range entities {
		if e.Type != "статья" {
			continue
		}
		m := legalArticleRefRe.FindStringSubmatch(normalizeName(e.Name))
		if m == nil {
			continue
		}
		list, ok := anchors["статья "+m[1]]
		if !ok {
			continue
		}
		canon, ok := narrowArticleAnchor(list, e.Name)
		if !ok {
			continue
		}
		transientByID[e.ID] = canon
	}
	if len(transientByID) == 0 {
		return nil, nil
	}

	relations, err := u.Store.AllRelations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list relations: %w", err)
	}
	for _, r := range relations {
		typ := strings.ToLower(strings.TrimSpace(r.Type))
		if typ != "interprets" && typ != "clarifies" {
			continue
		}
		canon, ok := transientByID[r.Dst]
		if !ok {
			continue
		}
		newRel := r
		newRel.Dst = canon.ID
		newRel.ID = RelationID(r.Src, canon.ID, r.Type)
		if newRel.ID == r.ID {
			continue
		}
		if err := u.Store.ReplaceRelation(ctx, r.ID, newRel); err != nil {
			return nil, fmt.Errorf("replace %q: %w", r.ID, err)
		}
		touched[r.Src] = struct{}{}
		touched[canon.ID] = struct{}{}
	}

	byID := entitiesByID(entities)
	var extra []graphstore.Entity
	for id, canon := range transientByID {
		if err := u.Store.DeleteEntity(ctx, id); err != nil {
			return nil, fmt.Errorf("delete transient %q: %w", id, err)
		}
		touched[canon.ID] = struct{}{}
		transientChunks := []string(nil)
		if e, ok := byID[id]; ok {
			transientChunks = e.SourceChunks
		}
		extra = append(extra, graphstore.Entity{
			ID:           canon.ID,
			Name:         canon.Name,
			Type:         canon.Type,
			SourceChunks: transientChunks,
		})
	}
	return extra, nil
}

func entitiesByID(entities []graphstore.Entity) map[string]graphstore.Entity {
	out := make(map[string]graphstore.Entity, len(entities))
	for _, e := range entities {
		out[e.ID] = e
	}
	return out
}

// codeNameFromRef returns the raw code reference that follows an article
// reference ("статья 8 ГК РФ" -> "ГК РФ"), or "" when the reference names
// no code ("статья 8.1" -> "").
func codeNameFromRef(name string) string {
	trimmed := strings.TrimSpace(name)
	m := legalArticleRefRe.FindStringSubmatch(trimmed)
	if m == nil {
		return ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, m[0]))
	return strings.Trim(rest, ". ,;:")
}

// legalCodeKey reduces a code reference to a comparable form: lowercase
// with all punctuation removed, so "ГК РФ" and the "гк-рф" article-id
// prefix collapse to the same key ("гкрф").
func legalCodeKey(s string) string {
	return legalCodeNonAlnumRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "")
}

// narrowArticleAnchor picks the canonical anchor for a legal-article
// reference among the anchors of the same article number. When the
// reference names a code, only anchors whose identity contains that code
// qualify. An unambiguous single match is required, otherwise no anchor is
// returned (fail-open: the transient reference is left untouched rather
// than mis-anchored to another code's article).
func narrowArticleAnchor(list []graphstore.Entity, refName string) (graphstore.Entity, bool) {
	if code := codeNameFromRef(refName); code != "" {
		codeKey := legalCodeKey(code)
		var narrowed []graphstore.Entity
		for _, a := range list {
			if strings.Contains(legalCodeKey(a.Name+" "+a.Description), codeKey) {
				narrowed = append(narrowed, a)
			}
		}
		if len(narrowed) == 0 {
			return graphstore.Entity{}, false
		}
		list = narrowed
	}
	if len(list) != 1 {
		return graphstore.Entity{}, false
	}
	return list[0], true
}
