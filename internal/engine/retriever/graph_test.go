package retriever

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

type fakeGraphStore struct {
	entities      map[string]graphstore.Entity // normalized name -> entity
	neighbors     map[string][]graphstore.Entity
	relations     map[string][]graphstore.Relation
	communities   []graphstore.Community
	allErr        error
	matchErr      error
	neighborsErr  error
	communityErr  error
	staleCount    int
	staleCountErr error
	refreshErr    error
	refreshCalls  int
	matchedNames  []string
	neighborCalls []string
}

func (f *fakeGraphStore) MatchEntities(ctx context.Context, names []string, at ...time.Time) ([]graphstore.Entity, error) {
	f.matchedNames = names
	if f.matchErr != nil {
		return nil, f.matchErr
	}
	seen := map[string]struct{}{}
	var out []graphstore.Entity
	for _, n := range names {
		e, ok := f.entities[normalizeName(n)]
		if !ok {
			continue
		}
		if _, dup := seen[e.ID]; dup {
			continue
		}
		seen[e.ID] = struct{}{}
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeGraphStore) Neighbors(ctx context.Context, entityID string, hops int, at ...time.Time) ([]graphstore.Entity, []graphstore.Relation, error) {
	f.neighborCalls = append(f.neighborCalls, entityID)
	if f.neighborsErr != nil {
		return nil, nil, f.neighborsErr
	}
	return f.neighbors[entityID], f.relations[entityID], nil
}

func (f *fakeGraphStore) CommunitiesFor(ctx context.Context, ids []string) ([]graphstore.Community, error) {
	if f.communityErr != nil {
		return nil, f.communityErr
	}
	return f.communities, nil
}

func (f *fakeGraphStore) AllCommunities(ctx context.Context) ([]graphstore.Community, error) {
	if f.allErr != nil {
		return nil, f.allErr
	}
	return f.communities, nil
}

func (f *fakeGraphStore) StaleCommunityCount(ctx context.Context) (int, error) {
	if f.staleCountErr != nil {
		return 0, f.staleCountErr
	}
	return f.staleCount, nil
}

func (f *fakeGraphStore) RefreshStaleCommunities(ctx context.Context) (int, error) {
	f.refreshCalls++
	if f.refreshErr != nil {
		return 0, f.refreshErr
	}
	f.staleCount = 0
	return 0, nil
}

func normalizeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}

func TestQueryNgrams(t *testing.T) {
	got := queryNgrams("Acme Corp status", 2)
	sort.Strings(got)
	want := []string{
		"Acme Corp", "Acme.Corp", "Corp status", "Corp.status",
		"status", "Acme", "Corp",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("queryNgrams = %v, want %v", got, want)
	}
}

func TestQueryNgramsEmpty(t *testing.T) {
	if got := queryNgrams("   ", 4); len(got) != 0 {
		t.Fatalf("expected no ngrams for blank query, got %v", got)
	}
}

func TestQueryNgramsDedupsCaseInsensitive(t *testing.T) {
	got := queryNgrams("Acme acme", 1)
	if len(got) != 1 {
		t.Fatalf("expected case-insensitive dedup, got %v", got)
	}
}

func TestQueryNgramsEmitsDottedSymbolForms(t *testing.T) {
	// Go method/qualified names contain '.', which the tokenizer treats as
	// a separator; the dot-joined variant must still be emitted so exact
	// entity-name matching can link "IntCalc.Add".
	got := queryNgrams("how does IntCalc.Add round", 3)
	found := false
	for _, g := range got {
		if g == "IntCalc.Add" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dotted symbol n-gram 'IntCalc.Add', got %v", got)
	}
}

func TestRetrieveEntityLinkingBySymbolName(t *testing.T) {
	// A query mentioning a Go method by its dotted symbol name must link
	// the code-method entity, even though prose n-grams never contain '.'.
	chunks := []vector.Chunk{
		{ID: "c", RefDocID: "doc-code", Text: "package calc\nfunc round(n int) int { return n }", FilePath: "code/calc.go", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	method := graphstore.Entity{
		ID:           "intcalc-add|code-method",
		Name:         "IntCalc.Add",
		Type:         "code-method",
		SourceChunks: []string{"c"},
	}
	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{"intcalc.add": method},
	}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "how does IntCalc.Add round numbers", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(gs.neighborCalls) == 0 {
		t.Fatalf("expected entity linking to reach neighbor expansion, got %v", gs.neighborCalls)
	}
	if gs.neighborCalls[0] != method.ID {
		t.Fatalf("expected expansion from %q, got %v", method.ID, gs.neighborCalls)
	}
	found := false
	for _, sc := range got {
		if sc.Chunk.ID == "c" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the code chunk to be retrieved, got %+v", got)
	}
}

func TestRetrieveNeighborExpansionAlongCallGraph(t *testing.T) {
	// The callee chunk shares no terms/embedding with the query, so hybrid
	// alone would never find it; only neighbor expansion along the CALLS
	// edge from the linked caller function should surface it.
	chunks := []vector.Chunk{
		{ID: "main", RefDocID: "doc-main", Text: "package main\nfunc Sum(nums []int) int { return total }", FilePath: "code/main.go", Embedding: []float32{1, 0}},
		{ID: "add", RefDocID: "doc-add", Text: "package calc\nfunc Add(a, b int) int { return a + b }", FilePath: "code/add.go", Embedding: []float32{0, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	sum := graphstore.Entity{ID: "sum|code-function", Name: "Sum", Type: "code-function", SourceChunks: []string{"main"}}
	add := graphstore.Entity{ID: "add|code-function", Name: "Add", Type: "code-function", SourceChunks: []string{"add"}}

	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{"sum": sum},
		neighbors: map[string][]graphstore.Entity{
			"sum|code-function": {add},
		},
		relations: map[string][]graphstore.Relation{
			"sum|code-function": {{ID: "r1", Src: "sum|code-function", Dst: "add|code-function", Type: "CALLS", Weight: 3}},
		},
	}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "Sum callers and usage", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	found := false
	for _, sc := range got {
		if sc.Chunk.ID == "add" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected CALLS-edge neighbor expansion to surface chunk 'add', got %+v", got)
	}
}

func TestRetrieveMixedCodeAndProseGraphKeepsFusion(t *testing.T) {
	// Code and prose entities coexist in one graph: hybrid surfaces the
	// prose chunk, graph-neighbor expansion along the call-graph surfaces
	// the code chunk, and RRF fusion keeps both without error.
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "acme status", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
		{ID: "add", RefDocID: "doc-add", Text: "func Add(a, b int) int { return a + b }", FilePath: "code/add.go", Embedding: []float32{0, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	acme := graphstore.Entity{ID: "acme|org", Name: "Acme"}
	sum := graphstore.Entity{ID: "sum|code-function", Name: "Sum", Type: "code-function", SourceChunks: []string{"add"}}
	add := graphstore.Entity{ID: "add|code-function", Name: "Add", Type: "code-function", SourceChunks: []string{"add"}}

	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{"acme": acme, "sum": sum},
		neighbors: map[string][]graphstore.Entity{
			"sum|code-function": {add},
		},
		relations: map[string][]graphstore.Relation{
			"sum|code-function": {{ID: "r1", Src: "sum|code-function", Dst: "add|code-function", Type: "CALLS", Weight: 3}},
		},
	}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "Acme status and Sum", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	ids := make(map[string]bool)
	for _, sc := range got {
		ids[sc.Chunk.ID] = true
	}
	if !ids["a"] {
		t.Fatalf("expected prose chunk 'a' via hybrid retrieval, got %+v", got)
	}
	if !ids["add"] {
		t.Fatalf("expected code chunk 'add' via call-graph neighbor expansion, got %+v", got)
	}
}

func TestRetrieveGraphNeighborExpansionSurfacesUnrelatedChunk(t *testing.T) {
	// "b" shares no terms/embedding with the query, so hybrid alone would
	// never find it; only graph-neighbor expansion from the linked entity
	// "Acme" should surface it.
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "acme status", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc-b", Text: "unrelated payload", FilePath: "notes/b.md", Embedding: []float32{0, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	acme := graphstore.Entity{ID: "acme|org", Name: "Acme"}
	bob := graphstore.Entity{ID: "bob|person", Name: "Bob", SourceChunks: []string{"b"}}

	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{"acme": acme},
		neighbors: map[string][]graphstore.Entity{
			"acme|org": {bob},
		},
		relations: map[string][]graphstore.Relation{
			"acme|org": {{ID: "r1", Src: "acme|org", Dst: "bob|person", Weight: 2}},
		},
	}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	found := false
	for _, sc := range got {
		if sc.Chunk.ID == "b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected graph-neighbor expansion to surface chunk 'b', got %+v", got)
	}
}

func TestRetrieveCommunitySummaryIncluded(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "acme status", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	acme := graphstore.Entity{ID: "acme|org", Name: "Acme"}
	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{"acme": acme},
		communities: []graphstore.Community{
			{ID: "c1", Title: "Acme cluster", Summary: "Acme and its partners.", Members: []string{"acme|org"}},
		},
	}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	found := false
	for _, sc := range got {
		if sc.Chunk.ID == "community:c1" {
			found = true
			if sc.Chunk.Source != "graph" {
				t.Errorf("expected community chunk source 'graph', got %q", sc.Chunk.Source)
			}
			if sc.Chunk.Text != "Acme and its partners." {
				t.Errorf("expected community chunk text to be the summary, got %q", sc.Chunk.Text)
			}
		}
	}
	if !found {
		t.Fatalf("expected community summary to be included, got %+v", got)
	}
}

func TestRetrieveGraphDegradesToHybridWhenNoEntitiesLinked(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	gs := &fakeGraphStore{entities: map[string]graphstore.Entity{}}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected pure-hybrid result when no entities link, got %+v", got)
	}
}

func TestRetrieveGraphNilSkipsGracefully(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  nil,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected unaffected hybrid result with nil Graph, got %+v", got)
	}
}

func TestRetrieveGraphDegradesOnMatchEntitiesError(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "acme status", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	gs := &fakeGraphStore{matchErr: errors.New("boom")}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve should not error on MatchEntities failure: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected fallback to hybrid result, got %+v", got)
	}
}

func TestRetrieveGraphDegradesOnNeighborsAndCommunitiesError(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "acme status", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	acme := graphstore.Entity{ID: "acme|org", Name: "Acme"}
	gs := &fakeGraphStore{
		entities:     map[string]graphstore.Entity{"acme": acme},
		neighborsErr: errors.New("neighbors down"),
		communityErr: errors.New("communities down"),
	}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve should not error when Neighbors/CommunitiesFor fail: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected fallback to hybrid result, got %+v", got)
	}
}

func TestRetrieveGraphNeighborChunkLookupRequiresBM25(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "acme status", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}

	acme := graphstore.Entity{ID: "acme|org", Name: "Acme"}
	bob := graphstore.Entity{ID: "bob|person", Name: "Bob", SourceChunks: []string{"b"}}
	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{"acme": acme},
		neighbors: map[string][]graphstore.Entity{
			"acme|org": {bob},
		},
		relations: map[string][]graphstore.Relation{
			"acme|org": {{ID: "r1", Src: "acme|org", Dst: "bob|person", Weight: 2}},
		},
	}

	r := New(Config{
		Vector: vs,
		BM25:   nil,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: false,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected only dense result without BM25 chunk resolver, got %+v", got)
	}
}

func TestRetrieveClosedChunksInvisibleToBM25AndGraphLegs(t *testing.T) {
	active := vector.Chunk{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Embedding: []float32{1, 0}}
	closed := vector.Chunk{ID: "old", RefDocID: "doc-old", Text: "apple orchard", FilePath: "chats/old.md", Embedding: []float32{1, 0}, ValidTo: "2026-08-01T00:00:00Z"}
	vs := &fakeVectorStore{chunks: []vector.Chunk{active, closed}}
	idx := bm25.New()
	idx.Rebuild([]vector.Chunk{active}, 1)

	acme := graphstore.Entity{ID: "acme|org", Name: "Acme"}
	neighbor := graphstore.Entity{ID: "bob|person", Name: "Bob", SourceChunks: []string{"old"}}
	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{"acme": acme},
		neighbors: map[string][]graphstore.Entity{
			"acme|org": {neighbor},
		},
		relations: map[string][]graphstore.Relation{
			"acme|org": {{ID: "r1", Src: "acme|org", Dst: "bob|person", Weight: 2}},
		},
		communities: []graphstore.Community{
			{ID: "c1", Level: 1, Members: []string{"acme|org"}, Summary: "Acme and partners."},
		},
	}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "apple Acme", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	for _, sc := range got {
		if sc.Chunk.ID == "old" {
			t.Fatalf("closed chunk surfaced through retrieval: %+v", got)
		}
	}
	foundActive, foundCommunity := false, false
	for _, sc := range got {
		switch sc.Chunk.ID {
		case "a":
			foundActive = true
		case "community:c1":
			foundCommunity = true
		}
	}
	if !foundActive || !foundCommunity {
		t.Fatalf("expected active chunk and community summary to surface, got %+v", got)
	}
}

func TestRetrieveRefreshesStaleCommunitiesPreQuery(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "acme status", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	acme := graphstore.Entity{ID: "acme|org", Name: "Acme"}
	gs := &fakeGraphStore{
		entities:   map[string]graphstore.Entity{"acme": acme},
		staleCount: 3,
	}

	now := time.Now()
	clock := &fakeClock{t: now}
	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
		Clock:  clock.Now,
	})

	if _, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10}); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if gs.refreshCalls != 1 {
		t.Fatalf("first query: refreshCalls = %d, want 1 (stale > threshold)", gs.refreshCalls)
	}

	if _, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10}); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if gs.refreshCalls != 1 {
		t.Fatalf("second query within throttle window: refreshCalls = %d, want still 1", gs.refreshCalls)
	}

	gs.staleCount = 2
	clock.t = clock.t.Add(staleCommunityMinInterval + time.Second)
	if _, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10}); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if gs.refreshCalls != 2 {
		t.Fatalf("query after throttle window: refreshCalls = %d, want 2", gs.refreshCalls)
	}
}

func TestRetrieveSkipsRefreshBelowThreshold(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "acme status", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	gs := &fakeGraphStore{staleCount: 0}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	if _, err := r.Retrieve(context.Background(), "acme status", Options{K: 10}); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if gs.refreshCalls != 0 {
		t.Fatalf("refreshCalls = %d, want 0 (no stale communities)", gs.refreshCalls)
	}
}

func TestRetrieveRefreshErrorServesStaleSummaries(t *testing.T) {
	roots := []graphstore.Community{
		{ID: "c1", Level: 1, Title: "t1", Summary: "stale but served", SourceChunks: []string{"a"}},
	}
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "acme status", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	acme := graphstore.Entity{ID: "acme|org", Name: "Acme"}
	gs := &fakeGraphStore{
		entities:    map[string]graphstore.Entity{"acme": acme},
		communities: roots,
		staleCount:  1,
		refreshErr:  errors.New("refresh down"),
	}

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve must not fail on refresh error: %v", err)
	}
	if gs.refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d, want 1", gs.refreshCalls)
	}
	found := false
	for _, sc := range got {
		if sc.Chunk.ID == "community:c1" && strings.Contains(sc.Chunk.Text, "stale but served") {
			found = true
		}
	}
	if !found {
		t.Fatalf("stale summary should be served as-is after failed refresh, got %+v", got)
	}
}

type fakeClock struct {
	t time.Time
}

func (f *fakeClock) Now() time.Time { return f.t }
