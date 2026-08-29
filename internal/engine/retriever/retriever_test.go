package retriever

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
)

type fakeVectorStore struct {
	chunks   []vector.Chunk
	queryErr error
}

func (f *fakeVectorStore) EnsureDim(ctx context.Context, dim int) error            { return nil }
func (f *fakeVectorStore) Upsert(ctx context.Context, chunks []vector.Chunk) error { return nil }
func (f *fakeVectorStore) ReplaceByDoc(ctx context.Context, docID string, chunks []vector.Chunk) error {
	return nil
}
func (f *fakeVectorStore) DeleteByDoc(ctx context.Context, docID string) error    { return nil }
func (f *fakeVectorStore) SoftCloseByDoc(ctx context.Context, docID string) error { return nil }
func (f *fakeVectorStore) SetSuperseded(ctx context.Context, chunkIDs []string, byRefDocID string) error {
	return nil
}
func (f *fakeVectorStore) ClearSupersededBy(ctx context.Context, refDocID string) error { return nil }
func (f *fakeVectorStore) ClearSupersededOnDoc(ctx context.Context, docID string) error { return nil }
func (f *fakeVectorStore) AllForBM25(ctx context.Context) ([]vector.Chunk, error) {
	return activeChunks(f.chunks), nil
}

func (f *fakeVectorStore) ChunksByDoc(ctx context.Context, docID string) ([]vector.Chunk, error) {
	var out []vector.Chunk
	for _, c := range f.chunks {
		if c.RefDocID == docID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeVectorStore) DocHash(ctx context.Context, docID string) (string, bool, error) {
	return "", false, nil
}

func (f *fakeVectorStore) SetDocHash(ctx context.Context, docID, hash string) error { return nil }

func (f *fakeVectorStore) Query(ctx context.Context, vec []float32, k int, filter vector.Filter) ([]vector.ScoredChunk, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	var scored []vector.ScoredChunk
	for _, c := range f.chunks {
		if c.ValidTo != "" {
			continue
		}
		if !filter.Matches(c.Source, c.Metadata) {
			continue
		}
		score := cosine(vec, c.Embedding)
		if score <= 0 {
			// Mimics a real dense store: an unrelated/zero-signal chunk is
			// not "found" by the query, only genuinely similar ones are.
			continue
		}
		scored = append(scored, vector.ScoredChunk{Chunk: c, Score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > k {
		scored = scored[:k]
	}
	return scored, nil
}

func activeChunks(chunks []vector.Chunk) []vector.Chunk {
	var out []vector.Chunk
	for _, c := range chunks {
		if c.ValidTo == "" {
			out = append(out, c)
		}
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

type fakeEmbedder struct {
	err error
	// vec returns an embedding for a given text, so tests can control
	// which chunk each subquery is "closest" to.
	vec func(text string) []float32
}

func (f fakeEmbedder) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.vec(t)
	}
	return out, nil
}

// constVec returns an embedder func that maps every text to the same
// vector, letting a test control similarity purely via chunk embeddings.
func constVec(v []float32) func(string) []float32 {
	return func(string) []float32 { return v }
}

func TestRetrieveDenseAndBM25FusedByRRF(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc-b", Text: "banana grove", FilePath: "notes/b.md", Embedding: []float32{0, 1}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected results")
	}
	if got[0].Chunk.ID != "a" {
		t.Errorf("expected doc 'a' (dense+bm25 match) to rank first, got %q", got[0].Chunk.ID)
	}
}

func TestRetrieveDegradesWhenExpanderFails(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	r := New(Config{
		Vector: vs,
		Chat:   fakeChat{err: errors.New("llm down")},
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: false,
	})

	got, err := r.Retrieve(context.Background(), "q", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve should not error on expander failure: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected fallback to original query to still retrieve chunk a, got %+v", got)
	}
}

func TestRetrieveDegradesWhenDenseEmbedFails(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard fruit", FilePath: "notes/a.md"},
	}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: &fakeVectorStore{chunks: chunks},
		BM25:   idx,
		Embed:  fakeEmbedder{err: errors.New("embed down")},
		Hybrid: true,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve should not error when embedding fails: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected BM25-only fallback to still retrieve chunk a, got %+v", got)
	}
}

func TestRetrieveDegradesWhenBM25Unavailable(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	r := New(Config{
		Vector: &fakeVectorStore{chunks: chunks},
		BM25:   nil,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
	})

	got, err := r.Retrieve(context.Background(), "q", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve should not error with nil BM25: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected dense-only fallback to still retrieve chunk a, got %+v", got)
	}
}

func TestRetrieveHybridOffExcludesBM25OnlyMatches(t *testing.T) {
	// "b" is only findable via BM25 (zero embedding never wins dense scoring).
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc-b", Text: "kiwi", FilePath: "notes/b.md", Embedding: []float32{0, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: false,
	})

	got, err := r.Retrieve(context.Background(), "kiwi", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	for _, sc := range got {
		if sc.Chunk.ID == "b" {
			t.Fatalf("hybrid=false should exclude BM25-only matches, but got %+v", got)
		}
	}
}

func TestRetrievePerDocCoverageCap(t *testing.T) {
	var chunks []vector.Chunk
	for i := 0; i < 5; i++ {
		chunks = append(chunks, vector.Chunk{
			ID: string(rune('a' + i)), RefDocID: "doc-1", Text: "shared", FilePath: "notes/x.md",
			Embedding: []float32{1, 0},
		})
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector:    vs,
		BM25:      idx,
		Embed:     fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid:    true,
		PerDocCap: 2,
	})

	got, err := r.Retrieve(context.Background(), "shared", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected per-doc cap to limit to 2 chunks from doc-1, got %d: %+v", len(got), got)
	}
}

func TestRetrieveAuthorityBonusPromotesApprovedNotes(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "plain", RefDocID: "doc-plain", Text: "shared topic", FilePath: "chats/general.md", Embedding: []float32{1, 0}},
		{ID: "approved", RefDocID: "doc-approved", Text: "shared topic", FilePath: "notes/approved/x.md", Embedding: []float32{0.99, 0.01}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		AuthorityBonus: map[string]float64{
			"notes/":          0.15,
			"notes/approved/": 0.30,
		},
	})

	got, err := r.Retrieve(context.Background(), "shared topic", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both chunks, got %d", len(got))
	}
	if got[0].Chunk.ID != "approved" {
		t.Errorf("expected authority bonus to promote 'approved' above 'plain' despite lower raw similarity, got order %v", []string{got[0].Chunk.ID, got[1].Chunk.ID})
	}
}

func TestRetrieveMetadataFilter(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "shared", FilePath: "notes/a.md", Embedding: []float32{1, 0}, Metadata: map[string]string{"project": "X"}},
		{ID: "b", RefDocID: "doc-b", Text: "shared", FilePath: "notes/b.md", Embedding: []float32{1, 0}, Metadata: map[string]string{"project": "Y"}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
	})

	got, err := r.Retrieve(context.Background(), "shared", Options{
		K:      10,
		Filter: vector.Filter{Metadata: map[string]string{"project": "X"}},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected only project=X chunk 'a', got %+v", got)
	}
}

func TestRetrieveNoResultsReturnsEmptyNotError(t *testing.T) {
	r := New(Config{
		Vector: &fakeVectorStore{},
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: false,
	})
	got, err := r.Retrieve(context.Background(), "anything", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no results, got %+v", got)
	}
}

func TestRetrieveDegradesWhenVectorQueryErrors(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard fruit", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: &fakeVectorStore{chunks: chunks, queryErr: errors.New("query down")},
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve should not error when the vector store query fails: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected BM25-only fallback to still retrieve chunk a, got %+v", got)
	}
}

func TestRetrieveHybridOnIncludesBM25OnlyMatches(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc-b", Text: "kiwi", FilePath: "notes/b.md", Embedding: []float32{0, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
	})

	got, err := r.Retrieve(context.Background(), "kiwi", Options{K: 10})
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
		t.Fatalf("hybrid=true should surface BM25-only matches, got %+v", got)
	}
}

func TestRetrieveSupersededChunkPenalizedButRetrievable(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "new", RefDocID: "doc-new", Text: "acme quarterly revenue grew", FilePath: "notes/new.md", Embedding: []float32{1, 0}},
		{ID: "old", RefDocID: "doc-old", Text: "acme quarterly revenue grew", FilePath: "chats/old.md", Embedding: []float32{1, 0}, SupersededBy: "doc-new"},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
	})

	got, err := r.Retrieve(context.Background(), "acme quarterly revenue", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("superseded chunk must stay retrievable, got %d results: %+v", len(got), got)
	}
	if got[0].Chunk.ID != "new" || got[1].Chunk.ID != "old" {
		t.Fatalf("expected active chunk first and superseded chunk second, got %+v", got)
	}
	if got[0].Score != 1 {
		t.Errorf("active chunk must not receive a penalty, score = %v, want 1", got[0].Score)
	}
	if got[1].Score >= got[0].Score {
		t.Errorf("superseded chunk must score below the active chunk, got %v vs %v", got[1].Score, got[0].Score)
	}
}

func TestRetrieveSupersededPenaltyDoesNotExcludeOnlyMatch(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "only", RefDocID: "doc-old", Text: "rare fact", FilePath: "chats/old.md", Embedding: []float32{1, 0}, SupersededBy: "doc-new"},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
	})

	got, err := r.Retrieve(context.Background(), "rare fact", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "only" {
		t.Fatalf("superseded chunk must surface when it is the only match, got %+v", got)
	}
}

type fakeVersioner struct{ v int }

func (f *fakeVersioner) CorpusVersion(ctx context.Context) (int, error) { return f.v, nil }

type fakeChunkLister struct{ chunks []vector.Chunk }

func (f *fakeChunkLister) AllForBM25(ctx context.Context) ([]vector.Chunk, error) {
	return f.chunks, nil
}

func TestBM25RefreshDropsClosedChunksOnCorpusVersionBump(t *testing.T) {
	active := vector.Chunk{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md"}
	closed := vector.Chunk{ID: "old", RefDocID: "doc-old", Text: "apple orchard", FilePath: "chats/old.md", ValidTo: "2026-08-01T00:00:00Z"}

	idx := bm25.New()
	idx.Rebuild([]vector.Chunk{active, closed}, 1)
	versioner := &fakeVersioner{v: 2}
	lister := &fakeChunkLister{chunks: []vector.Chunk{active}}

	if err := idx.Refresh(context.Background(), versioner, lister); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	results := idx.Search("apple", 10)
	if len(results) == 0 {
		t.Fatalf("expected active chunk in BM25 results after refresh")
	}
	for _, res := range results {
		if res.ID == "old" {
			t.Fatalf("closed chunk still indexed after corpus_version bump: %+v", results)
		}
	}
}
