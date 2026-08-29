package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
)

// fakeReranker records the candidates it was called with and returns a
// caller-supplied result/error, letting tests control reranker behavior
// without depending on the real llm-backed implementation.
type fakeReranker struct {
	reorder func([]vector.ScoredChunk) []vector.ScoredChunk
	err     error
	called  bool
}

func (f *fakeReranker) Rerank(ctx context.Context, query string, cands []vector.ScoredChunk) ([]vector.ScoredChunk, error) {
	f.called = true
	if f.err != nil {
		return cands, f.err
	}
	if f.reorder != nil {
		return f.reorder(cands), nil
	}
	return cands, nil
}

func TestRetrieveAppliesRerankerAfterRRF(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc-b", Text: "banana", FilePath: "notes/b.md", Embedding: []float32{0.9, 0.1}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	reranker := &fakeReranker{reorder: func(cands []vector.ScoredChunk) []vector.ScoredChunk {
		reversed := make([]vector.ScoredChunk, len(cands))
		for i, c := range cands {
			reversed[len(cands)-1-i] = c
		}
		return reversed
	}}

	r := New(Config{
		Vector:   vs,
		BM25:     idx,
		Embed:    fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid:   true,
		Reranker: reranker,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if !reranker.called {
		t.Fatal("expected reranker to be called")
	}
	if len(got) != 2 || got[0].Chunk.ID != "b" || got[1].Chunk.ID != "a" {
		t.Fatalf("expected reranker's reversed order [b a], got %+v", got)
	}
}

func TestRetrieveDegradesWhenRerankerFails(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	reranker := &fakeReranker{err: errors.New("reranker down")}

	r := New(Config{
		Vector:   vs,
		Embed:    fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid:   false,
		Reranker: reranker,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve should not error when reranker fails: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("expected fail-open to original order, got %+v", got)
	}
}

func TestRetrieveNilRerankerLeavesOrderUnchanged(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	r := New(Config{
		Vector: &fakeVectorStore{chunks: chunks},
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: false,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("got %+v", got)
	}
}
