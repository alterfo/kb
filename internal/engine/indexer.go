package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine/chunk"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/store/vector"
)

// Embedder embeds chunk texts. Satisfied by *llm.Client.
type Embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// Config wires the Indexer's dependencies. Graph and Embed are optional:
// a nil Graph skips knowledge-graph updates, a nil Embed indexes chunks for
// BM25 only (fail-open, no vectors).
type Config struct {
	Root         string
	Vector       vector.Store
	Graph        *graph.GraphUpdater
	Embed        Embedder
	EmbedModel   string
	ChunkSize    int
	ChunkOverlap int
}

// Result summarizes a Reindex/BuildAll run.
type Result struct {
	Indexed int
	Skipped int
	Removed int
}

const blastRadiusMinShared = 1

// Indexer ties ingest output (markdown files under Root, or Documents
// written directly via an API sink) to the vector store and knowledge
// graph: chunking, embedding, delete-then-insert, and incremental graph
// updates.
type Indexer struct {
	root       string
	vector     vector.Store
	graph      *graph.GraphUpdater
	embed      Embedder
	embedModel string
	chunker    *chunk.TextChunker
	chat       *chunk.ChatChunker

	mu       sync.Mutex
	chatBuf  map[string][]threadChatMsg
	bulkChat bool
	apiRefs  map[string]struct{}
}

type threadChatMsg struct {
	rel    string
	doc    connector.Document
	apiFed bool
}

func NewIndexer(cfg Config) *Indexer {
	return &Indexer{
		root:       cfg.Root,
		vector:     cfg.Vector,
		graph:      cfg.Graph,
		embed:      cfg.Embed,
		embedModel: cfg.EmbedModel,
		chunker:    chunk.NewTextChunker(cfg.ChunkSize, cfg.ChunkOverlap),
		chat:       chunk.NewChatChunker(0),
		apiRefs:    map[string]struct{}{},
	}
}

// InferSource returns the virtual-collection name for a KB_ROOT-relative
// path: the first path segment, matching how Sink implementations lay
// documents out as ROOT/<source>/<id>.md. Used as a fallback when a
// document's frontmatter has no explicit source (e.g. a manually dropped
// note).
func InferSource(relPath string) string {
	relPath = strings.TrimPrefix(filepath.ToSlash(relPath), "/")
	if i := strings.Index(relPath, "/"); i >= 0 {
		return relPath[:i]
	}
	return ""
}

// AddOrUpdateDocument (re)indexes the markdown file at path: parses its
// frontmatter, chunks the body, embeds it (fail-open), and replaces the
// document's chunks in the vector store and knowledge graph. path may be
// absolute or relative to Root.
func (ix *Indexer) AddOrUpdateDocument(ctx context.Context, path string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	_, err := ix.addOrUpdateAbs(ctx, ix.resolveAbs(path))
	return err
}

// RemoveDocument deletes a document's chunks from the vector store and
// garbage-collects its knowledge-graph contribution. path may be absolute
// or relative to Root, and need not still exist on disk.
func (ix *Indexer) RemoveDocument(ctx context.Context, path string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	rel, err := ix.relFromAbs(ix.resolveAbs(path))
	if err != nil {
		return err
	}
	return ix.removeByRefDocID(ctx, DocRefID(rel))
}

// IndexDocument indexes a Document directly, without reading it from disk.
// It mirrors the path a Sink would have written the document to
// (source/sanitized-id.md), so it stays consistent with a subsequent
// filesystem-driven reindex of the same document. Intended for API-fed
// writes (APISink -> server -> Indexer) that never touch the filesystem.
func (ix *Indexer) IndexDocument(ctx context.Context, doc connector.Document) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if doc.Source == "" {
		return fmt.Errorf("engine: IndexDocument: document missing source (id=%q)", doc.ID)
	}
	if doc.ID == "" {
		return fmt.Errorf("engine: IndexDocument: document missing id (source=%q)", doc.Source)
	}
	rel := doc.Source + "/" + sanitizeID(doc.ID) + ".md"
	if _, err := ix.indexParsedDocument(ctx, rel, doc, true); err != nil {
		return err
	}
	ix.apiRefs[DocRefID(rel)] = struct{}{}
	return nil
}

// RemoveDocumentBySourceID removes the document with the given source and
// id, mirroring the rel layout used by IndexDocument. Used by the API-fed
// tombstone path (APISink -> server -> Indexer).
func (ix *Indexer) RemoveDocumentBySourceID(ctx context.Context, source, id string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	rel := source + "/" + sanitizeID(id) + ".md"
	return ix.removeByRefDocID(ctx, DocRefID(rel))
}

// PruneSource removes every indexed document belonging to source whose
// sanitized document id is not in seen, mirroring FileSink.Prune for the
// API-fed path. When prefixes is non-empty, only documents whose raw id
// matches one of the prefixes are eligible for removal. It returns the
// number of documents removed.
func (ix *Indexer) PruneSource(ctx context.Context, source string, seen map[string]struct{}, prefixes ...string) (int, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	all, err := ix.vector.AllForBM25(ctx)
	if err != nil {
		return 0, fmt.Errorf("engine: prune %q: list chunks: %w", source, err)
	}
	seenIDs := make(map[string]struct{}, len(seen))
	for id := range seen {
		seenIDs[sanitizeID(id)] = struct{}{}
	}
	prefix := source + "/"
	stale := map[string]struct{}{}
	for _, c := range all {
		if !strings.HasPrefix(c.RefDocID, prefix) {
			continue
		}
		if _, ok := seenIDs[c.RefDocID[len(prefix):]]; ok {
			continue
		}
		if len(prefixes) > 0 && !idInPrefixes(c.Metadata["id"], prefixes) {
			// No stored id (indexed before the field existed) or a
			// non-matching prefix: incremental-only, preserve.
			continue
		}
		stale[c.RefDocID] = struct{}{}
	}
	removed := 0
	for ref := range stale {
		if err := ix.removeByRefDocID(ctx, ref); err != nil {
			return removed, fmt.Errorf("engine: prune %q: %w", source, err)
		}
		removed++
	}
	return removed, nil
}

func idInPrefixes(id string, prefixes []string) bool {
	if id == "" {
		return false
	}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// Reindex re-indexes a single path (file or directory subtree) when path is
// non-empty, or the whole tree (equivalent to BuildAll) when path is empty.
// A path that no longer exists on disk is treated as a removal.
func (ix *Indexer) Reindex(ctx context.Context, path string) (Result, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if path == "" {
		return ix.buildAll(ctx)
	}

	abs := ix.resolveAbs(path)
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		rel, rerr := ix.relFromAbs(abs)
		if rerr != nil {
			return Result{}, rerr
		}
		if err := ix.removeByRefDocID(ctx, DocRefID(rel)); err != nil {
			return Result{}, err
		}
		return Result{Removed: 1}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("engine: reindex %q: %w", path, err)
	}

	if !info.IsDir() {
		skipped, err := ix.addOrUpdateAbs(ctx, abs)
		if err != nil {
			return Result{}, err
		}
		if skipped {
			return Result{Skipped: 1}, nil
		}
		return Result{Indexed: 1}, nil
	}

	var res Result
	ix.beginBulkChat()
	defer ix.endBulkChat()
	if ix.graph != nil {
		ix.graph.BeginBulk()
		defer func() {
			if err := ix.graph.EndBulk(ctx); err != nil {
				log.Printf("engine: reindex %q: end bulk graph: %v (continuing)", path, err)
			}
		}()
	}
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != abs && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isMarkdown(p) {
			return nil
		}
		skipped, err := ix.addOrUpdateAbs(ctx, p)
		if err != nil {
			return err
		}
		if skipped {
			res.Skipped++
		} else {
			res.Indexed++
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("engine: reindex %q: %w", path, err)
	}
	if err := ix.flushChatThreads(ctx); err != nil {
		return Result{}, fmt.Errorf("engine: reindex %q: chat threads: %w", path, err)
	}
	return res, nil
}

// BuildAll walks the whole Root tree, (re)indexing every markdown file, and
// then garbage-collects any previously indexed document whose file is no
// longer present (no "dead" source directories left behind).
func (ix *Indexer) BuildAll(ctx context.Context) (Result, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.buildAll(ctx)
}

func (ix *Indexer) buildAll(ctx context.Context) (Result, error) {
	var res Result
	live := map[string]struct{}{}
	// API-fed documents (sync --api) never touch the filesystem; seed them
	// into live so the garbage-collection pass below does not drop them.
	for ref := range ix.apiRefs {
		live[ref] = struct{}{}
	}
	ix.beginBulkChat()
	defer ix.endBulkChat()
	if ix.graph != nil {
		ix.graph.BeginBulk()
		defer func() {
			if err := ix.graph.EndBulk(ctx); err != nil {
				log.Printf("engine: build all: end bulk graph: %v (continuing)", err)
			}
		}()
	}

	err := filepath.WalkDir(ix.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != ix.root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isMarkdown(p) {
			return nil
		}
		rel, err := ix.relFromAbs(p)
		if err != nil {
			return err
		}
		skipped, err := ix.addOrUpdateAbs(ctx, p)
		if err != nil {
			return err
		}
		live[DocRefID(rel)] = struct{}{}
		if skipped {
			res.Skipped++
		} else {
			res.Indexed++
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("engine: build all: %w", err)
	}
	if err := ix.flushChatThreads(ctx); err != nil {
		return Result{}, fmt.Errorf("engine: build all: chat threads: %w", err)
	}

	all, err := ix.vector.AllForBM25(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("engine: build all: list chunks: %w", err)
	}
	// API-fed documents have no file on disk; their chunks carry the apifed
	// marker, so a full reindex from a fresh process (empty in-memory
	// apiRefs) must keep them rather than garbage-collect them.
	for _, c := range all {
		if c.Metadata["apifed"] == "1" {
			live[c.RefDocID] = struct{}{}
		}
	}
	stale := map[string]struct{}{}
	for _, c := range all {
		if _, ok := live[c.RefDocID]; !ok {
			stale[c.RefDocID] = struct{}{}
		}
	}
	for refDocID := range stale {
		if err := ix.removeByRefDocID(ctx, refDocID); err != nil {
			return Result{}, fmt.Errorf("engine: build all: gc %q: %w", refDocID, err)
		}
		res.Removed++
	}
	return res, nil
}

// addOrUpdateAbs (re)indexes the file at abs and reports whether it was
// skipped because its content hash matched the last-indexed hash. Chat
// thread messages are never hashed: bulk-buffered thread gluing (see
// beginBulkChat) writes chunks later, in flushChatThreads, so recording a
// hash here would mark a message as indexed before it actually was.
func (ix *Indexer) addOrUpdateAbs(ctx context.Context, abs string) (bool, error) {
	rel, err := ix.relFromAbs(abs)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("engine: read %q: %w", rel, err)
	}

	doc, err := render.Parse(data)
	if err != nil {
		doc = connector.Document{Body: string(data)}
	}
	if doc.Source == "" {
		doc.Source = InferSource(rel)
	}

	refDocID := DocRefID(rel)
	hashable := doc.Kind != "message"
	var hash string
	if hashable {
		hash = contentHash(data)
		if prev, ok, err := ix.vector.DocHash(ctx, refDocID); err == nil && ok && prev == hash {
			return true, nil
		}
	}

	vectorsOK, err := ix.indexParsedDocument(ctx, rel, doc, false)
	if err != nil {
		return false, err
	}
	if hashable && vectorsOK {
		if err := ix.vector.SetDocHash(ctx, refDocID, hash); err != nil {
			return false, fmt.Errorf("engine: index %q: record content hash: %w", rel, err)
		}
	}
	return false, nil
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (ix *Indexer) indexParsedDocument(ctx context.Context, rel string, doc connector.Document, apiFed bool) (bool, error) {
	if doc.Kind == "message" {
		if tid, ok := frontmatterThread(doc); ok && tid != "" {
			if ix.bulkChat {
				key := doc.Source + "\x00" + tid
				ix.chatBuf[key] = append(ix.chatBuf[key], threadChatMsg{rel: rel, doc: doc, apiFed: apiFed})
				return true, nil
			}
			msgs, err := ix.threadGroupFromDisk(rel, doc, apiFed)
			if err != nil {
				return false, err
			}
			if err := ix.indexChatThread(ctx, msgs); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	var chunks []chunk.Chunk
	var err error
	isGoSource := strings.HasSuffix(strings.ToLower(rel), ".go") || docIsGoSource(doc)
	switch {
	case doc.Kind == "message":
		chunks, err = ix.chatChunks(doc)
	case doc.Kind == "code" || isGoSource:
		// Whole-body chunk: the code-graph extraction path (updater.go)
		// parses the file deterministically from the single chunk.
		chunks = []chunk.Chunk{{Text: doc.Body, Index: 0, TokenCount: chunk.EstimateTokens(doc.Body)}}
	default:
		chunks, err = ix.chunker.Chunk(doc.Body)
	}
	if err != nil {
		return false, fmt.Errorf("engine: chunk %q: %w", rel, err)
	}
	return ix.indexChunks(ctx, rel, doc, chunks, apiFed)
}

func docIsGoSource(doc connector.Document) bool {
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

// threadGroupFromDisk returns the full set of messages forming the thread
// doc belongs to: the freshly parsed doc plus every sibling message file on
// disk with the same source and thread. Re-indexing a single member must
// re-glue the whole reply chain; otherwise the siblings' text would silently
// disappear from the index, because their chunks live inside the glued chunk
// owned by the earliest message of the thread.
func (ix *Indexer) threadGroupFromDisk(rel string, doc connector.Document, apiFed bool) ([]threadChatMsg, error) {
	msgs := []threadChatMsg{{rel: rel, doc: doc, apiFed: apiFed}}
	tid, ok := frontmatterThread(doc)
	if !ok || tid == "" {
		return msgs, nil
	}
	srcDir := filepath.Join(ix.root, filepath.FromSlash(doc.Source))
	entries, err := os.ReadDir(srcDir)
	if errors.Is(err, os.ErrNotExist) {
		return msgs, nil
	}
	if err != nil {
		return nil, fmt.Errorf("engine: chat thread: read %q: %w", srcDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !isMarkdown(e.Name()) {
			continue
		}
		sibAbs := filepath.Join(srcDir, e.Name())
		sibRel, err := ix.relFromAbs(sibAbs)
		if err != nil {
			return nil, err
		}
		if sibRel == rel {
			// The freshly parsed member; its disk copy may be stale.
			continue
		}
		data, err := os.ReadFile(sibAbs)
		if err != nil {
			return nil, fmt.Errorf("engine: chat thread: read %q: %w", sibRel, err)
		}
		sib, err := render.Parse(data)
		if err != nil {
			continue
		}
		if sib.Kind != "message" {
			continue
		}
		sibTid, ok := frontmatterThread(sib)
		if !ok || sibTid != tid {
			continue
		}
		if sib.Source == "" {
			sib.Source = InferSource(sibRel)
		}
		msgs = append(msgs, threadChatMsg{rel: sibRel, doc: sib})
	}
	return msgs, nil
}

func frontmatterThread(doc connector.Document) (string, bool) {
	v, ok := doc.Frontmatter["thread"]
	if !ok {
		return "", false
	}
	s := fmt.Sprint(v)
	if s == "" {
		return "", false
	}
	return s, true
}

func (ix *Indexer) indexChunks(ctx context.Context, rel string, doc connector.Document, chunks []chunk.Chunk, apiFed bool) (bool, error) {
	refDocID := DocRefID(rel)

	prev, err := ix.vector.ChunksByDoc(ctx, refDocID)
	if err != nil {
		return false, fmt.Errorf("engine: index %q: list existing chunks: %w", rel, err)
	}

	oldChunkIDs := make([]string, 0, len(prev))
	lastByIndex := make(map[int]vector.Chunk, len(prev))
	versionCount := make(map[int]int, len(prev))
	for _, c := range prev {
		oldChunkIDs = append(oldChunkIDs, c.ID)
		versionCount[c.ChunkIndex]++
		lastByIndex[c.ChunkIndex] = c
	}

	vecChunks := make([]vector.Chunk, len(chunks))
	texts := make([]string, len(chunks))
	metadata := frontmatterMetadata(doc.Frontmatter)
	if metadata == nil {
		metadata = map[string]string{}
	}
	// Store the raw document id so a scoped prune can match prefixes
	// (RefDocID holds the sanitized id and cannot be reversed).
	metadata["id"] = doc.ID
	if doc.Kind != "" {
		// Route the graph extraction path (generic / legal / chat) from
		// the chunk metadata, the same signal the indexer itself uses.
		metadata["kind"] = doc.Kind
	}
	if apiFed {
		metadata["apifed"] = "1"
	}
	for i, c := range chunks {
		cm := metadata
		if len(c.Metadata) > 0 {
			merged := make(map[string]string, len(metadata)+len(c.Metadata))
			for k, v := range metadata {
				merged[k] = v
			}
			for k, v := range c.Metadata {
				merged[k] = v
			}
			cm = merged
		}
		vecChunks[i] = vector.Chunk{
			ID:         versionedChunkID(refDocID, c.Index, versionCount[c.Index]),
			RefDocID:   refDocID,
			Text:       c.Text,
			FilePath:   rel,
			FileName:   filepath.Base(rel),
			Source:     doc.Source,
			TokenCount: c.TokenCount,
			ChunkIndex: c.Index,
			Replaces:   lastByIndex[c.Index].ID,
			Metadata:   cm,
		}
		texts[i] = c.Text
	}

	vectorsOK := true
	if ix.embed != nil && len(texts) > 0 {
		vecs, embErr := ix.embed.Embed(ctx, ix.embedModel, texts)
		if embErr == nil && len(vecs) == len(vecChunks) {
			if len(vecs) > 0 {
				if err := ix.vector.EnsureDim(ctx, len(vecs[0])); err != nil {
					return false, fmt.Errorf("engine: index %q: %w", rel, err)
				}
			}
			for i := range vecChunks {
				vecChunks[i].Embedding = vecs[i]
			}
		} else {
			for i := range vecChunks {
				old, ok := lastByIndex[vecChunks[i].ChunkIndex]
				if ok && len(old.Embedding) > 0 && old.Text == vecChunks[i].Text {
					vecChunks[i].Embedding = old.Embedding
				}
			}
			for i := range vecChunks {
				if len(vecChunks[i].Embedding) == 0 {
					vectorsOK = false
					break
				}
			}
		}
	}

	if err := ix.vector.ReplaceByDoc(ctx, refDocID, vecChunks); err != nil {
		return false, fmt.Errorf("engine: index %q: replace chunks: %w", rel, err)
	}
	if !apiFed {
		delete(ix.apiRefs, refDocID)
	}

	if ix.graph != nil {
		touched, err := ix.graph.UpdateDocument(ctx, refDocID, vecChunks, oldChunkIDs...)
		if err != nil {
			return false, fmt.Errorf("engine: index %q: update graph: %w", rel, err)
		}
		if err := ix.vector.ClearSupersededBy(ctx, refDocID); err != nil {
			log.Printf("engine: index %q: clear superseded_by: %v (continuing)", rel, err)
		}
		if err := ix.vector.ClearSupersededOnDoc(ctx, refDocID); err != nil {
			log.Printf("engine: index %q: clear own superseded_by: %v (continuing)", rel, err)
		}
		if len(touched) > 0 && ix.graph.Store != nil {
			overlapping, err := ix.graph.Store.OverlappingChunks(ctx, touched, refDocID, blastRadiusMinShared)
			if err != nil {
				log.Printf("engine: index %q: blast-radius overlap query: %v (continuing)", rel, err)
			} else if len(overlapping) > 0 {
				if err := ix.vector.SetSuperseded(ctx, overlapping, refDocID); err != nil {
					log.Printf("engine: index %q: blast-radius supersede: %v (continuing)", rel, err)
				}
			}
		}
	}
	return vectorsOK, nil
}

func (ix *Indexer) beginBulkChat() {
	ix.bulkChat = true
	ix.chatBuf = map[string][]threadChatMsg{}
}

func (ix *Indexer) endBulkChat() {
	ix.bulkChat = false
	ix.chatBuf = nil
}

func (ix *Indexer) indexChatThread(ctx context.Context, msgs []threadChatMsg) error {
	if len(msgs) == 0 {
		return nil
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].doc.UpdatedAt.Before(msgs[j].doc.UpdatedAt) })
	root := msgs[0]
	tid, _ := frontmatterThread(root.doc)
	key := root.doc.Source + "\x00" + tid

	if !ix.bulkChat {
		// Incremental path: a previous bulk reindex may have glued this
		// thread under the earliest message's refDocID. Drop that chunk so
		// re-indexing a single member never leaves the old glued text (now
		// stale or duplicated) behind.
		if err := ix.removeGluedThreadChunks(ctx, key); err != nil {
			return fmt.Errorf("engine: chat thread: drop glued %q: %w", key, err)
		}
	}

	chatMsgs := make([]chunk.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		tid, _ := frontmatterThread(m.doc)
		chatMsgs = append(chatMsgs, chunk.ChatMessage{
			Text:      m.doc.Body,
			User:      frontmatterString(m.doc, "user"),
			ThreadID:  tid,
			Timestamp: m.doc.UpdatedAt,
		})
	}
	chunks := ix.chat.Chunk(chatMsgs)
	if len(msgs) > 1 {
		for i := range chunks {
			if chunks[i].Metadata == nil {
				chunks[i].Metadata = map[string]string{}
			}
			chunks[i].Metadata["glued_thread"] = key
		}
	}
	if _, err := ix.indexChunks(ctx, root.rel, root.doc, chunks, root.apiFed); err != nil {
		return err
	}
	for _, m := range msgs[1:] {
		if err := ix.removeByRefDocID(ctx, DocRefID(m.rel)); err != nil {
			return fmt.Errorf("engine: chat thread: drop %q: %w", m.rel, err)
		}
	}
	return nil
}

// removeGluedThreadChunks removes every chunk a previous bulk glue produced
// for the group key (source\x00thread-id), keyed by the glued_thread
// metadata marker. RefDocIDs of the removed chunks are tracked so a group
// spanning multiple chunks is only touched once.
func (ix *Indexer) removeGluedThreadChunks(ctx context.Context, key string) error {
	all, err := ix.vector.AllForBM25(ctx)
	if err != nil {
		return fmt.Errorf("engine: list chunks for glued thread %q: %w", key, err)
	}
	removed := map[string]struct{}{}
	for _, c := range all {
		if c.Metadata["glued_thread"] != key {
			continue
		}
		if _, ok := removed[c.RefDocID]; ok {
			continue
		}
		removed[c.RefDocID] = struct{}{}
		if err := ix.removeByRefDocID(ctx, c.RefDocID); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Indexer) flushChatThreads(ctx context.Context) error {
	keys := make([]string, 0, len(ix.chatBuf))
	for k := range ix.chatBuf {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		group := ix.chatBuf[k]
		delete(ix.chatBuf, k)
		if err := ix.indexChatThread(ctx, group); err != nil {
			return err
		}
	}
	return nil
}

// chatChunks builds a single ChatMessage from a message document and chunks
// it with the thread-aware ChatChunker, so a chat message is never split on
// sentence boundaries and its thread context lands in chunk metadata.
func (ix *Indexer) chatChunks(doc connector.Document) ([]chunk.Chunk, error) {
	var threadID, parentID string
	if v, ok := doc.Frontmatter["thread"]; ok {
		threadID = fmt.Sprint(v)
	}
	if v, ok := doc.Frontmatter["parent_id"]; ok {
		parentID = fmt.Sprint(v)
	}
	msgs := []chunk.ChatMessage{{
		Text:      doc.Body,
		User:      frontmatterString(doc, "user"),
		ThreadID:  threadID,
		ParentID:  parentID,
		Timestamp: doc.UpdatedAt,
	}}
	return ix.chat.Chunk(msgs), nil
}

func frontmatterString(doc connector.Document, key string) string {
	if v, ok := doc.Frontmatter[key]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func (ix *Indexer) removeByRefDocID(ctx context.Context, refDocID string) error {
	delete(ix.apiRefs, refDocID)
	var chunkIDs []string
	if ix.graph != nil {
		chunks, err := ix.vector.ChunksByDoc(ctx, refDocID)
		if err != nil {
			return fmt.Errorf("engine: remove %q: list chunks: %w", refDocID, err)
		}
		for _, c := range chunks {
			chunkIDs = append(chunkIDs, c.ID)
		}
	}

	if err := ix.vector.DeleteByDoc(ctx, refDocID); err != nil {
		return fmt.Errorf("engine: remove %q: %w", refDocID, err)
	}
	if err := ix.vector.ClearSupersededBy(ctx, refDocID); err != nil {
		log.Printf("engine: remove %q: clear superseded_by: %v (continuing)", refDocID, err)
	}

	if ix.graph != nil && len(chunkIDs) > 0 {
		if err := ix.graph.RemoveDocument(ctx, refDocID, chunkIDs); err != nil {
			return fmt.Errorf("engine: remove %q: update graph: %w", refDocID, err)
		}
	}
	return nil
}

func (ix *Indexer) resolveAbs(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(ix.root, path)
}

func (ix *Indexer) relFromAbs(abs string) (string, error) {
	rootAbs, err := filepath.Abs(ix.root)
	if err != nil {
		return "", fmt.Errorf("engine: resolve root %q: %w", ix.root, err)
	}
	absAbs, err := filepath.Abs(abs)
	if err != nil {
		return "", fmt.Errorf("engine: resolve path %q: %w", abs, err)
	}
	rootAbs = filepath.Clean(rootAbs)
	absAbs = filepath.Clean(absAbs)
	if absAbs != rootAbs && !strings.HasPrefix(absAbs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("engine: path %q escapes root %q", abs, ix.root)
	}
	rel, err := filepath.Rel(rootAbs, absAbs)
	if err != nil {
		return "", fmt.Errorf("engine: relativize %q: %w", abs, err)
	}
	return filepath.ToSlash(rel), nil
}

// DocRefID derives the ref_doc_id used for a document's chunks from its
// root-relative path (extension stripped) — exported so callers outside
// this package (e.g. the web dashboard, to look up a document's chunks) can
// derive the same id without duplicating the transform.
func DocRefID(relPath string) string {
	return strings.TrimSuffix(relPath, filepath.Ext(relPath))
}

func versionedChunkID(refDocID string, index, existingVersions int) string {
	if existingVersions == 0 {
		return fmt.Sprintf("%s#%d", refDocID, index)
	}
	return fmt.Sprintf("%s#%d#%d", refDocID, index, existingVersions+1)
}

func isMarkdown(p string) bool {
	return strings.EqualFold(filepath.Ext(p), ".md")
}

func frontmatterMetadata(fm map[string]any) map[string]string {
	if len(fm) == 0 {
		return nil
	}
	out := make(map[string]string, len(fm))
	for k, v := range fm {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
			out[k] = ""
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return out
}

func sanitizeID(id string) string {
	var b strings.Builder
	replaced := false
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
			replaced = true
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	// Must match internal/sink/filesink.go's sanitizeID: a source named
	// "." or ".." must not escape the sink root through
	// filepath.Join(root, sanitizeID(sourceName)).
	if b.String() == "." || b.String() == ".." {
		return "_"
	}
	// Must match internal/sink/filesink.go's sanitizeID: distinct ids whose
	// sanitized forms collapse together would overwrite each other on
	// disk, so a short hash of the raw id keeps colliding ids distinct.
	if replaced {
		sum := sha256.Sum256([]byte(id))
		fmt.Fprintf(&b, "-%x", sum[:4])
	}
	return b.String()
}
