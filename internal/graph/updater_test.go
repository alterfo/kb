package graph

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

func newTestUpdater(t *testing.T, chat ChatClient) (*GraphUpdater, graphstore.Store) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store := sqlite.NewGraphStore(db)
	extractor := NewExtractor(chat, "model")
	summarizer := NewSummarizer(chat, "model")
	return NewGraphUpdater(store, extractor, summarizer), store
}

// scriptedChat serves a fixed queue of responses, one per Chat call; once
// exhausted it returns the last response repeatedly (extraction + gleaning +
// summary calls all funnel through the same Chat method).
type scriptedChat struct {
	responses []string
	i         int
	calls     []llm.ChatRequest
}

func (s *scriptedChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	s.calls = append(s.calls, req)
	if len(s.responses) == 0 {
		return llm.ChatResponse{Content: `{"entities":[],"relations":[]}`}, nil
	}
	idx := s.i
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}
	s.i++
	return llm.ChatResponse{Content: s.responses[idx]}, nil
}

func aliceKnowsBobJSON() string {
	return `{"entities":[{"name":"Alice","type":"person","description":"eng"},{"name":"Bob","type":"person","description":"pm"}],"relations":[{"source":"Alice","target":"Bob","type":"knows","description":"colleagues"}]}`
}

// barrierChat extracts one entity per call, named after the trailing
// CHUNK-<X> marker of the user message, and blocks every call until the
// configured barrier count of calls is in flight. It records the maximum
// observed number of concurrent calls.
type barrierChat struct {
	mu        sync.Mutex
	inFlight  int
	maxFlight int
	barrier   int
	ready     chan struct{}
}

func (b *barrierChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	b.mu.Lock()
	b.inFlight++
	if b.inFlight > b.maxFlight {
		b.maxFlight = b.inFlight
	}
	block := b.inFlight <= b.barrier
	if b.inFlight == b.barrier {
		close(b.ready)
	}
	content := ""
	if len(req.Messages) > 0 {
		content = req.Messages[len(req.Messages)-1].Content
	}
	b.mu.Unlock()

	if block {
		<-b.ready
	}
	fields := strings.Fields(content)
	name := "E"
	if len(fields) > 0 {
		name = strings.TrimPrefix(fields[len(fields)-1], "CHUNK-")
	}

	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()
	return llm.ChatResponse{Content: `{"entities":[{"name":"E-` + name + `","type":"thing","description":""}],"relations":[]}`}, nil
}

func TestUpdateDocumentParallelExtraction(t *testing.T) {
	chat := &barrierChat{barrier: 3, ready: make(chan struct{})}
	updater, store := newTestUpdater(t, chat)
	updater.WithExtractConcurrency(3)
	ctx := context.Background()

	chunks := []vector.Chunk{
		{ID: "c1", RefDocID: "doc1", Text: "alpha CHUNK-A"},
		{ID: "c2", RefDocID: "doc1", Text: "beta CHUNK-B"},
		{ID: "c3", RefDocID: "doc1", Text: "gamma CHUNK-C"},
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	if chat.maxFlight != 3 {
		t.Errorf("max in-flight extraction calls = %d, want 3 (parallelism)", chat.maxFlight)
	}
	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 3 {
		t.Fatalf("got %d entities, want 3: %+v", len(entities), entities)
	}
	byName := map[string]graphstore.Entity{}
	for _, e := range entities {
		byName[e.Name] = e
	}
	wantChunks := map[string]string{"E-A": "c1", "E-B": "c2", "E-C": "c3"}
	for name, wantChunk := range wantChunks {
		e, ok := byName[name]
		if !ok {
			t.Errorf("entity %s missing", name)
			continue
		}
		if len(e.SourceChunks) != 1 || e.SourceChunks[0] != wantChunk {
			t.Errorf("entity %s source chunks = %v, want [%s]", name, e.SourceChunks, wantChunk)
		}
	}
}

func TestUpdateDocumentCreatesEntitiesAndRelations(t *testing.T) {
	chat := &scriptedChat{responses: []string{aliceKnowsBobJSON(), `{"title":"Alice & Bob","summary":"work together"}`}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("got %d entities, want 2: %+v", len(entities), entities)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 {
		t.Fatalf("got %d relations, want 1: %+v", len(relations), relations)
	}
}

func TestUpdateDocumentStampsLLMRelationProvenance(t *testing.T) {
	chat := &scriptedChat{responses: []string{aliceKnowsBobJSON(), `{"title":"T","summary":"S"}`}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 {
		t.Fatalf("got %d relations, want 1: %+v", len(relations), relations)
	}
	got := relations[0]
	if got.Provenance != "extraction" || got.ExtractorVersion != "model" {
		t.Fatalf("LLM relation provenance = %+v, want extraction/model", got)
	}
	if got.Confidence != 1.0 {
		t.Fatalf("LLM relation confidence = %v, want default 1.0", got.Confidence)
	}
}

func TestUpdateDocumentReturnsTouchedEntities(t *testing.T) {
	chat := &scriptedChat{responses: []string{aliceKnowsBobJSON(), `{"title":"Alice & Bob","summary":"work together"}`}}
	updater, _ := newTestUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}
	touched, err := updater.UpdateDocument(ctx, "doc1", chunks)
	if err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}
	want := []string{EntityID("Alice", "person"), EntityID("Bob", "person")}
	sort.Strings(want)
	sort.Strings(touched)
	if !reflect.DeepEqual(touched, want) {
		t.Fatalf("touched entities = %v, want %v", touched, want)
	}
}

func TestUpdateDocumentReturnsNoTouchedWhenNoEntities(t *testing.T) {
	updater, _ := newTestUpdater(t, nil)
	ctx := context.Background()

	touched, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "plain text without entities"}})
	if err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}
	if len(touched) != 0 {
		t.Fatalf("touched = %v, want none", touched)
	}
}

func TestUpdateDocumentReindexDoesNotDuplicate(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		aliceKnowsBobJSON(), `{"title":"T1","summary":"S1"}`,
		aliceKnowsBobJSON(), `{"title":"T2","summary":"S2"}`,
	}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("first UpdateDocument: %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("second UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("got %d entities after reindex, want 2 (no duplicates): %+v", len(entities), entities)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 {
		t.Fatalf("got %d relations after reindex, want 1 (no duplicates): %+v", len(relations), relations)
	}
	for _, r := range relations {
		if len(r.SourceChunks) != 1 {
			t.Fatalf("relation SourceChunks = %v, want exactly [c1] (not accumulated)", r.SourceChunks)
		}
	}

	ids := make([]string, len(entities))
	for i, e := range entities {
		ids[i] = e.ID
	}
	communities, err := store.CommunitiesFor(ctx, ids)
	if err != nil {
		t.Fatalf("CommunitiesFor: %v", err)
	}
	if len(communities) != 1 || communities[0].Title != "T1" || communities[0].Summary != "S1" {
		t.Fatalf("got community %+v, want unchanged content to reuse T1/S1 without a second LLM summary call", communities)
	}
	if chat.i != 3 {
		t.Fatalf("Chat call count = %d, want 3 (extraction, summary, re-extraction — summary reused, not regenerated)", chat.i)
	}
}

func TestUpdateDocumentRemovalPrunesEntitiesAndRelations(t *testing.T) {
	chat := &scriptedChat{responses: []string{aliceKnowsBobJSON(), `{"title":"T","summary":"S"}`}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	if err := updater.RemoveDocument(ctx, "doc1", []string{"c1"}); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("got %d entities after removal, want 0: %+v", len(entities), entities)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 0 {
		t.Fatalf("got %d relations after removal, want 0: %+v", len(relations), relations)
	}
	communities, err := store.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	if len(communities) != 0 {
		t.Fatalf("got %d communities after removal, want 0 (orphaned communities must be pruned): %+v", len(communities), communities)
	}
}

func TestUpdateDocumentBuildsCommunityWithSummary(t *testing.T) {
	chat := &scriptedChat{responses: []string{aliceKnowsBobJSON(), `{"title":"Alice & Bob","summary":"work together"}`}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	ids := make([]string, len(entities))
	for i, e := range entities {
		ids[i] = e.ID
	}
	communities, err := store.CommunitiesFor(ctx, ids)
	if err != nil {
		t.Fatalf("CommunitiesFor: %v", err)
	}
	if len(communities) != 1 {
		t.Fatalf("got %d communities, want 1: %+v", len(communities), communities)
	}
	if communities[0].Title != "Alice & Bob" || communities[0].Summary != "work together" {
		t.Fatalf("got community %+v, want title/summary from LLM", communities[0])
	}
}

func TestUpdateDocumentUnaffectedCommunityKeepsSummaryUnregenerated(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"entities":[{"name":"Carol","type":"person"},{"name":"Dave","type":"person"}],"relations":[{"source":"Carol","target":"Dave","type":"knows"}]}`,
		`{"title":"Carol & Dave","summary":"unrelated pair"}`,
		aliceKnowsBobJSON(),
		`{"title":"Alice & Bob","summary":"work together"}`,
	}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	if _, err := updater.UpdateDocument(ctx, "doc0", []vector.Chunk{{ID: "c0", RefDocID: "doc0", Text: "Carol knows Dave."}}); err != nil {
		t.Fatalf("UpdateDocument(doc0): %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}); err != nil {
		t.Fatalf("UpdateDocument(doc1): %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	var carolID string
	for _, e := range entities {
		if e.Name == "Carol" {
			carolID = e.ID
		}
	}
	if carolID == "" {
		t.Fatalf("Carol entity not found: %+v", entities)
	}

	communities, err := store.CommunitiesFor(ctx, []string{carolID})
	if err != nil {
		t.Fatalf("CommunitiesFor: %v", err)
	}
	if len(communities) != 1 || communities[0].Summary != "unrelated pair" {
		t.Fatalf("Carol/Dave community changed unexpectedly: %+v", communities)
	}
}

func TestUpdateDocumentNilStoreFails(t *testing.T) {
	u := NewGraphUpdater(nil, nil, nil)
	if _, err := u.UpdateDocument(context.Background(), "doc", nil); err == nil {
		t.Fatalf("expected error for nil store")
	}
}

func TestRemoveDocumentNilStoreFails(t *testing.T) {
	u := NewGraphUpdater(nil, nil, nil)
	if err := u.RemoveDocument(context.Background(), "doc", []string{"c1"}); err == nil {
		t.Fatalf("expected error for nil store")
	}
}

func TestRemoveDocumentEmptyChunkIDsNoop(t *testing.T) {
	updater, store := newTestUpdater(t, nil)
	ctx := context.Background()

	if err := updater.RemoveDocument(ctx, "doc1", nil); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}
	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("got %+v, want empty", entities)
	}
}

func TestUpdateDocumentExtractorErrorFailsOpenPerChunk(t *testing.T) {
	updater, store := newTestUpdater(t, nil)
	ctx := context.Background()

	chunks := []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("got %+v, want empty (nil chat fails open with no entities)", entities)
	}
}

func bigCliqueJSON(t *testing.T) string {
	t.Helper()
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r"}
	ents := ""
	for i, n := range names {
		if i > 0 {
			ents += ","
		}
		ents += `{"name":"` + n + `","type":"person","description":"member"}`
	}
	var rels []string
	for c := 0; c < 6; c++ {
		i := c * 3
		pairs := [][2]string{{names[i], names[i+1]}, {names[i+1], names[i+2]}, {names[i], names[i+2]}}
		for _, p := range pairs {
			rels = append(rels, `{"source":"`+p[0]+`","target":"`+p[1]+`","type":"links","description":""}`)
		}
	}
	bridges := [][2]string{{"c", "d"}, {"f", "g"}, {"i", "j"}, {"l", "m"}, {"o", "p"}, {"r", "a"}}
	for _, b := range bridges {
		rels = append(rels, `{"source":"`+b[0]+`","target":"`+b[1]+`","type":"links","description":""}`)
	}
	return `{"entities":[` + ents + `],"relations":[` + strings.Join(rels, ",") + `]}`
}

type cliqueChat struct {
	extractions []string
	summary     string
	extractIdx  int
}

func (s *cliqueChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "summarize a cluster") {
			return llm.ChatResponse{Content: s.summary}, nil
		}
	}
	idx := s.extractIdx
	if idx >= len(s.extractions) {
		idx = len(s.extractions) - 1
	}
	s.extractIdx++
	return llm.ChatResponse{Content: s.extractions[idx]}, nil
}

func TestUpdateDocumentLeidenMultiLevelCommunities(t *testing.T) {
	chat := &cliqueChat{extractions: []string{bigCliqueJSON(t)}, summary: `{"title":"T","summary":"S"}`}

	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store := sqlite.NewGraphStore(db)
	extractor := NewExtractor(chat, "model")
	summarizer := NewSummarizer(chat, "model")
	updater := NewGraphUpdater(store, extractor, summarizer).
		WithCommunityDetector(LeidenDetector{})
	ctx := context.Background()

	chunk := vector.Chunk{ID: "doc0/c", RefDocID: "doc0", Text: "cliques"}
	if _, err := updater.UpdateDocument(ctx, "doc0", []vector.Chunk{chunk}); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 18 {
		t.Fatalf("got %d entities, want 18: %+v", len(entities), entities)
	}
	allIDs := make([]string, 0, len(entities))
	for _, e := range entities {
		allIDs = append(allIDs, e.ID)
	}
	communities, err := store.CommunitiesFor(ctx, allIDs)
	if err != nil {
		t.Fatalf("CommunitiesFor: %v", err)
	}
	if len(communities) == 0 {
		t.Fatalf("no communities stored")
	}

	byLevel := map[int][]graphstore.Community{}
	for _, c := range communities {
		byLevel[c.Level] = append(byLevel[c.Level], c)
	}
	var levels []int
	for l := range byLevel {
		levels = append(levels, l)
	}
	sort.Ints(levels)
	if len(levels) < 2 {
		t.Fatalf("expected at least 2 hierarchy levels, got %v", levels)
	}
	for i, l := range levels {
		if l != i {
			t.Fatalf("levels not contiguous: %v", levels)
		}
	}

	level0 := byLevel[0]
	if len(level0) != 6 {
		t.Fatalf("level 0 has %d communities, want 6: %+v", len(level0), level0)
	}
	for _, c := range level0 {
		if len(c.Members) != 3 {
			t.Fatalf("level 0 community %s has %d members, want 3", c.ID, len(c.Members))
		}
	}
	for l := range byLevel {
		assertValidPartition(t, byLevel[l], entities, l)
	}
	if len(byLevel[levels[len(levels)-1]]) >= len(level0) {
		t.Fatalf("coarsest level %d has %d communities, want fewer than level 0's %d",
			levels[len(levels)-1], len(byLevel[levels[len(levels)-1]]), len(level0))
	}
}

func TestReindexMarksComponentStaleWithoutReDetecting(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		aliceKnowsBobJSON(), `{"title":"T1","summary":"S1"}`,
		aliceKnowsBobJSON(),
	}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	chunks := []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("first UpdateDocument: %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", chunks); err != nil {
		t.Fatalf("second UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	ids := make([]string, 0, len(entities))
	for _, e := range entities {
		ids = append(ids, e.ID)
	}
	communities, err := store.CommunitiesFor(ctx, ids)
	if err != nil {
		t.Fatalf("CommunitiesFor: %v", err)
	}
	if len(communities) != 1 {
		t.Fatalf("got %d communities, want 1: %+v", len(communities), communities)
	}
	if !communities[0].Stale {
		t.Fatalf("community after reindex not marked stale: %+v", communities[0])
	}
	if communities[0].Title != "T1" || communities[0].Summary != "S1" {
		t.Fatalf("community content changed on lazy marking: %+v", communities[0])
	}
	if chat.i != 3 {
		t.Fatalf("Chat call count = %d, want 3 (no summary regeneration on reindex)", chat.i)
	}
}

func TestRefreshStaleCommunitiesRecomputesOnlyStaleComponents(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"entities":[{"name":"Carol","type":"person"},{"name":"Dave","type":"person"}],"relations":[{"source":"Carol","target":"Dave","type":"knows"}]}`,
		`{"title":"Carol & Dave","summary":"unrelated pair"}`,
		aliceKnowsBobJSON(),
		`{"title":"Alice & Bob","summary":"work together"}`,
		aliceKnowsBobJSON(),
		`{"title":"Alice & Bob","summary":"newer summary"}`,
	}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	if _, err := updater.UpdateDocument(ctx, "doc0", []vector.Chunk{{ID: "c0", RefDocID: "doc0", Text: "Carol knows Dave."}}); err != nil {
		t.Fatalf("UpdateDocument(doc0): %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}); err != nil {
		t.Fatalf("UpdateDocument(doc1): %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c2", RefDocID: "doc1", Text: "Alice knows Bob."}}); err != nil {
		t.Fatalf("UpdateDocument(doc1) reindex: %v", err)
	}

	if n, err := store.StaleCommunityCount(ctx); err != nil || n != 1 {
		t.Fatalf("StaleCommunityCount = %d, %v; want 1 (only doc1's component)", n, err)
	}

	refreshed, err := updater.RefreshStaleCommunities(ctx)
	if err != nil {
		t.Fatalf("RefreshStaleCommunities: %v", err)
	}
	if refreshed != 1 {
		t.Fatalf("refreshed = %d, want 1", refreshed)
	}
	if n, err := store.StaleCommunityCount(ctx); err != nil || n != 0 {
		t.Fatalf("StaleCommunityCount after refresh = %d, %v; want 0", n, err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	allIDs := make([]string, 0, len(entities))
	for _, e := range entities {
		allIDs = append(allIDs, e.ID)
	}
	communities, err := store.CommunitiesFor(ctx, allIDs)
	if err != nil {
		t.Fatalf("CommunitiesFor: %v", err)
	}
	bySummary := map[string]int{}
	for _, c := range communities {
		if c.Summary != "" {
			bySummary[c.Summary]++
		}
	}
	if bySummary["newer summary"] != 1 {
		t.Fatalf("Alice/Bob community not refreshed to newer summary: %+v", communities)
	}
	if bySummary["unrelated pair"] != 1 {
		t.Fatalf("Carol/Dave community should be untouched (fresh component): %+v", communities)
	}
}

func TestRefreshStaleCommunitiesNoopWithoutStale(t *testing.T) {
	updater, _ := newTestUpdater(t, nil)
	ctx := context.Background()

	if n, err := updater.RefreshStaleCommunities(ctx); err != nil || n != 0 {
		t.Fatalf("RefreshStaleCommunities on empty store = %d, %v; want 0, nil", n, err)
	}
}

func TestUpdateDocumentReturnsOnlyNewEntities(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		aliceKnowsBobJSON(),
		`{"title":"Alice & Bob","summary":"work together"}`,
		`{"entities":[{"name":"Alice","type":"person"},{"name":"Carol","type":"person"}],"relations":[{"source":"Alice","target":"Carol","type":"knows"}]}`,
		`{"entities":[{"name":"Carol","type":"person"},{"name":"Dan","type":"person"}],"relations":[{"source":"Carol","target":"Dan","type":"knows"}]}`,
	}}
	updater, _ := newTestUpdater(t, chat)
	ctx := context.Background()

	if _, err := updater.UpdateDocument(ctx, "docA", []vector.Chunk{{ID: "a1", RefDocID: "docA", Text: "Alice knows Bob."}}); err != nil {
		t.Fatalf("UpdateDocument(docA): %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "docB", []vector.Chunk{{ID: "b1", RefDocID: "docB", Text: "Alice knows Carol."}}); err != nil {
		t.Fatalf("UpdateDocument(docB): %v", err)
	}
	touched, err := updater.UpdateDocument(ctx, "docB", []vector.Chunk{{ID: "b2", RefDocID: "docB", Text: "Carol knows Dan."}}, "b1")
	if err != nil {
		t.Fatalf("UpdateDocument(docB reindex): %v", err)
	}
	for _, id := range touched {
		if strings.HasPrefix(id, "alice") {
			t.Fatalf("touched includes dropped entity %q; want only entities the new version references", id)
		}
	}
	want := []string{EntityID("Carol", "person"), EntityID("Dan", "person")}
	sort.Strings(want)
	sort.Strings(touched)
	if !reflect.DeepEqual(touched, want) {
		t.Fatalf("touched = %v, want %v", touched, want)
	}
}

type failingCommunityStore struct {
	graphstore.Store
	failUpsert bool
}

func (f *failingCommunityStore) UpsertCommunities(ctx context.Context, communities []graphstore.Community) error {
	if f.failUpsert {
		return errors.New("boom")
	}
	return f.Store.UpsertCommunities(ctx, communities)
}

func TestRefreshStaleCommunitiesKeepsRowsOnFailure(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		aliceKnowsBobJSON(),
		`{"title":"Alice & Bob","summary":"work together"}`,
		aliceKnowsBobJSON(),
	}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c2", RefDocID: "doc1", Text: "Alice knows Bob."}}, "c1"); err != nil {
		t.Fatalf("UpdateDocument reindex: %v", err)
	}
	if n, err := store.StaleCommunityCount(ctx); err != nil || n != 1 {
		t.Fatalf("StaleCommunityCount = %d, %v; want 1", n, err)
	}

	updater.Store = &failingCommunityStore{Store: store, failUpsert: true}
	if _, err := updater.RefreshStaleCommunities(ctx); err == nil {
		t.Fatalf("RefreshStaleCommunities with failing upsert: want error, got nil")
	}
	updater.Store = store
	if n, err := store.StaleCommunityCount(ctx); err != nil || n != 1 {
		t.Fatalf("StaleCommunityCount after failed refresh = %d, %v; want 1 (rows preserved)", n, err)
	}
	if _, err := updater.RefreshStaleCommunities(ctx); err != nil {
		t.Fatalf("RefreshStaleCommunities retry: %v", err)
	}
	if n, err := store.StaleCommunityCount(ctx); err != nil || n != 0 {
		t.Fatalf("StaleCommunityCount after retry = %d, %v; want 0", n, err)
	}
}

func TestRefreshStaleCommunitiesDeletesOrphanStaleRows(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		aliceKnowsBobJSON(),
		`{"title":"Alice & Bob","summary":"work together"}`,
		`{"entities":[{"name":"Carol","type":"person"},{"name":"Dave","type":"person"}],"relations":[{"source":"Carol","target":"Dave","type":"knows"}]}`,
		`{"title":"Carol & Dave","summary":"unrelated pair"}`,
		aliceKnowsBobJSON(),
	}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}); err != nil {
		t.Fatalf("UpdateDocument(doc1): %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "doc2", []vector.Chunk{{ID: "c2", RefDocID: "doc2", Text: "Carol knows Dave."}}); err != nil {
		t.Fatalf("UpdateDocument(doc2): %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c3", RefDocID: "doc1", Text: "Alice knows Bob."}}, "c1"); err != nil {
		t.Fatalf("UpdateDocument(doc1 reindex): %v", err)
	}
	if err := updater.RemoveDocument(ctx, "doc1", []string{"c1", "c3"}); err != nil {
		t.Fatalf("RemoveDocument(doc1): %v", err)
	}

	if n, err := store.StaleCommunityCount(ctx); err != nil || n != 1 {
		t.Fatalf("StaleCommunityCount = %d, %v; want 1", n, err)
	}
	if _, err := updater.RefreshStaleCommunities(ctx); err != nil {
		t.Fatalf("RefreshStaleCommunities: %v", err)
	}
	if n, err := store.StaleCommunityCount(ctx); err != nil || n != 0 {
		t.Fatalf("StaleCommunityCount after refresh = %d, %v; want 0", n, err)
	}
	communities, err := store.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	for _, c := range communities {
		for _, m := range c.Members {
			if strings.HasPrefix(m, "alice") || strings.HasPrefix(m, "bob") {
				t.Fatalf("orphan stale community %q still references removed members", c.ID)
			}
		}
	}
}

func TestRefreshStaleCommunitiesEmptyGraphCleansStaleRows(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		aliceKnowsBobJSON(),
		`{"title":"Alice & Bob","summary":"work together"}`,
		aliceKnowsBobJSON(),
	}}
	updater, store := newTestUpdater(t, chat)
	ctx := context.Background()

	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c1", RefDocID: "doc1", Text: "Alice knows Bob."}}); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}
	if _, err := updater.UpdateDocument(ctx, "doc1", []vector.Chunk{{ID: "c2", RefDocID: "doc1", Text: "Alice knows Bob."}}, "c1"); err != nil {
		t.Fatalf("UpdateDocument reindex: %v", err)
	}
	if err := updater.RemoveDocument(ctx, "doc1", []string{"c1", "c2"}); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}
	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("entities = %+v, want none after removal", entities)
	}

	if _, err := updater.RefreshStaleCommunities(ctx); err != nil {
		t.Fatalf("RefreshStaleCommunities: %v", err)
	}
	if n, err := store.StaleCommunityCount(ctx); err != nil || n != 0 {
		t.Fatalf("StaleCommunityCount after refresh = %d, %v; want 0", n, err)
	}
	all, err := store.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("communities = %+v, want none", all)
	}
}
