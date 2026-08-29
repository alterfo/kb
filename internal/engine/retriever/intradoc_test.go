package retriever

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
)

func intraDocCorpus() []vector.Chunk {
	return []vector.Chunk{
		{ID: "d1-s0", RefDocID: "doc-d1", Text: "intro section alpha", FileName: "big.md", FilePath: "docs/big.md", Source: "confluence", ChunkIndex: 0, Embedding: []float32{0, 0, 0, 1}},
		{ID: "d1-s1", RefDocID: "doc-d1", Text: "requirements section alpha", FileName: "big.md", FilePath: "docs/big.md", Source: "confluence", ChunkIndex: 1, Embedding: []float32{1, 0, 0, 0}},
		{ID: "d1-s2", RefDocID: "doc-d1", Text: "limits appendix beta unrelated filler text here padding padding", FileName: "big.md", FilePath: "docs/big.md", Source: "confluence", ChunkIndex: 2, Embedding: []float32{0, 1, 0, 0}},
		{ID: "d1-s3", RefDocID: "doc-d1", Text: "disqualifying exception notes padding padding padding padding padding", FileName: "big.md", FilePath: "docs/big.md", Source: "confluence", ChunkIndex: 3, Embedding: []float32{0, 0, 1, 0}},
	}
}

func newIntraDocRetriever(t *testing.T, budget int) *Retriever {
	t.Helper()
	chunks := intraDocCorpus()
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)
	return New(Config{
		Vector:         vs,
		BM25:           idx,
		Hybrid:         true,
		Embed:          fakeEmbedder{vec: constVec([]float32{1, 0})},
		IntraDocBudget: budget,
	})
}

func ids(scs []vector.ScoredChunk) []string {
	out := make([]string, len(scs))
	for i, sc := range scs {
		out[i] = sc.Chunk.ID
	}
	return out
}

func TestIntraDocExpansionAddsSiblingSections(t *testing.T) {
	r := newIntraDocRetriever(t, 10000)

	got, err := r.Retrieve(context.Background(), "alpha", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	gotIDs := ids(got)
	for _, want := range []string{"d1-s1", "d1-s0", "d1-s2", "d1-s3"} {
		found := false
		for _, id := range gotIDs {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("section %s missing from expanded results %v", want, gotIDs)
		}
	}
}

func TestIntraDocBudgetCapsExpansion(t *testing.T) {
	r := newIntraDocRetriever(t, 20)

	got, err := r.Retrieve(context.Background(), "alpha", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	gotIDs := ids(got)
	if len(gotIDs) != 3 {
		t.Fatalf("budget should allow partial expansion (primary hits + one section), got %v", gotIDs)
	}
	if gotIDs[0] != "d1-s0" || gotIDs[1] != "d1-s1" {
		t.Fatalf("primary hits must keep their fused order, got %v", gotIDs)
	}
	for _, id := range gotIDs {
		if id == "d1-s3" {
			t.Fatalf("section beyond budget must be excluded: %v", gotIDs)
		}
	}
}

func TestIntraDocDisabledByDefault(t *testing.T) {
	chunks := intraDocCorpus()
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)
	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Hybrid: true,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
	})

	got, err := r.Retrieve(context.Background(), "alpha", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	for _, sc := range got {
		if sc.Chunk.ID == "d1-s2" || sc.Chunk.ID == "d1-s3" {
			t.Fatalf("expansion must be off by default, got %v", ids(got))
		}
	}
}

func TestIntraDocRespectsK(t *testing.T) {
	r := newIntraDocRetriever(t, 10000)

	got, err := r.Retrieve(context.Background(), "alpha", Options{K: 3})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) > 3 {
		t.Fatalf("expansion exceeded K: %v", ids(got))
	}
}

func TestIntraDocExpansionDeterministicOrder(t *testing.T) {
	r := newIntraDocRetriever(t, 10000)
	first, _ := r.Retrieve(context.Background(), "alpha", Options{K: 10})
	second, _ := r.Retrieve(context.Background(), "alpha", Options{K: 10})
	a, b := ids(first), ids(second)
	if len(a) != len(b) {
		t.Fatal("nondeterministic expansion length")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("nondeterministic order: %v vs %v", a, b)
		}
	}
}

func TestIntraDocExpansionSkipsSoftClosedSibling(t *testing.T) {
	chunks := intraDocCorpus()
	chunks = append(chunks, vector.Chunk{
		ID: "d1-closed", RefDocID: "doc-d1", Text: "old closed section", FileName: "big.md", FilePath: "docs/big.md", Source: "confluence", ChunkIndex: 4, Embedding: []float32{1, 0, 0, 0}, ValidTo: "2026-08-27T00:00:00Z",
	})
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)
	r := New(Config{
		Vector:         vs,
		BM25:           idx,
		Hybrid:         true,
		Embed:          fakeEmbedder{vec: constVec([]float32{1, 0})},
		IntraDocBudget: 10000,
	})

	got, err := r.Retrieve(context.Background(), "alpha", Options{K: 20})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	for _, sc := range got {
		if sc.Chunk.ID == "d1-closed" {
			t.Fatalf("soft-closed sibling was reintroduced: %v", ids(got))
		}
	}
}

func TestIntraDocExpansionRespectsFilter(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "d1-s0", RefDocID: "doc-d1", Text: "intro alpha", FileName: "big.md", FilePath: "docs/big.md", Source: "confluence", ChunkIndex: 0, Embedding: []float32{0, 0, 0, 1}, Metadata: map[string]string{"speaker": "bob"}},
		{ID: "d1-s1", RefDocID: "doc-d1", Text: "requirements alpha", FileName: "big.md", FilePath: "docs/big.md", Source: "confluence", ChunkIndex: 1, Embedding: []float32{1, 0, 0, 0}, Metadata: map[string]string{"speaker": "alice"}},
		{ID: "d1-s2", RefDocID: "doc-d1", Text: "limits beta filler padding", FileName: "big.md", FilePath: "docs/big.md", Source: "confluence", ChunkIndex: 2, Embedding: []float32{0, 1, 0, 0}, Metadata: map[string]string{"speaker": "alice"}},
		{ID: "d1-s3", RefDocID: "doc-d1", Text: "exception notes padding", FileName: "big.md", FilePath: "docs/big.md", Source: "confluence", ChunkIndex: 3, Embedding: []float32{0, 0, 1, 0}, Metadata: map[string]string{"speaker": "bob"}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)
	r := New(Config{
		Vector:         vs,
		BM25:           idx,
		Hybrid:         true,
		Embed:          fakeEmbedder{vec: constVec([]float32{1, 0})},
		IntraDocBudget: 10000,
	})

	got, err := r.Retrieve(context.Background(), "alpha", Options{K: 20, Filter: vector.Filter{Metadata: map[string]string{"speaker": "alice"}}})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	gotIDs := ids(got)
	for _, want := range []string{"d1-s1", "d1-s2"} {
		found := false
		for _, id := range gotIDs {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("matching sibling %s missing from filtered expansion %v", want, gotIDs)
		}
	}
	for _, exclude := range []string{"d1-s0", "d1-s3"} {
		for _, id := range gotIDs {
			if id == exclude {
				t.Fatalf("non-matching sibling %s leaked past filter: %v", exclude, gotIDs)
			}
		}
	}
}
