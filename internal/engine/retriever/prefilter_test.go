package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestDensePrefilterUsesCandidateQueryAndExcludesOutsideCandidates(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc-b", Text: "apple pie", Embedding: []float32{0.9, 0.1}},
		{ID: "c", RefDocID: "doc-c", Text: "carrot", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector:       vs,
		BM25:         idx,
		Embed:        fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid:       true,
		ANNPrefilter: true,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if vs.candidateQueryCall != 1 {
		t.Fatalf("candidate query calls = %d, want 1", vs.candidateQueryCall)
	}
	if vs.queryCalls != 0 {
		t.Fatalf("exhaustive query calls = %d, want 0", vs.queryCalls)
	}
	for _, sc := range got {
		if sc.Chunk.ID == "c" {
			t.Fatalf("chunk c was outside the prefilter candidate set but was retrieved: %+v", got)
		}
	}
	if len(vs.lastCandidates) == 0 {
		t.Fatalf("expected prefilter candidates, got none")
	}
}

func TestDensePrefilterFallsBackToExhaustiveOnCandidateError(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks, candidateQueryErr: errors.New("boom")}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector:       vs,
		BM25:         idx,
		Embed:        fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid:       true,
		ANNPrefilter: true,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if vs.candidateQueryCall != 1 {
		t.Fatalf("candidate query calls = %d, want 1", vs.candidateQueryCall)
	}
	if vs.queryCalls != 1 {
		t.Fatalf("exhaustive fallback calls = %d, want 1", vs.queryCalls)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("fallback result = %+v, want chunk a", got)
	}
}

func TestDensePrefilterDisabledUsesExhaustiveQuery(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", Embedding: []float32{1, 0}},
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

	if _, err := r.Retrieve(context.Background(), "apple", Options{K: 10}); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if vs.queryCalls != 1 {
		t.Fatalf("exhaustive query calls = %d, want 1", vs.queryCalls)
	}
	if vs.candidateQueryCall != 0 {
		t.Fatalf("candidate query calls = %d, want 0", vs.candidateQueryCall)
	}
}

func TestDensePrefilterIncludesEntitySourceChunks(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "entity-chunk", RefDocID: "doc-entity", Text: "acme project notes", Embedding: []float32{1, 0}},
		{ID: "unlinked", RefDocID: "doc-other", Text: "unrelated", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{
			"acme": {ID: "acme|org", Name: "Acme", Type: "org", SourceChunks: []string{"entity-chunk"}},
		},
	}

	r := New(Config{
		Vector:       vs,
		Graph:        gs,
		Embed:        fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid:       true,
		ANNPrefilter: true,
	})

	got, err := r.Retrieve(context.Background(), "Acme status", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if vs.candidateQueryCall != 1 {
		t.Fatalf("candidate query calls = %d, want 1", vs.candidateQueryCall)
	}
	foundEntity := false
	for _, sc := range got {
		if sc.Chunk.ID == "unlinked" {
			t.Fatalf("unlinked chunk was not in the entity candidate set but was retrieved: %+v", got)
		}
		if sc.Chunk.ID == "entity-chunk" {
			foundEntity = true
		}
	}
	if !foundEntity {
		t.Fatalf("entity source chunk not retrieved, got %+v", got)
	}
}

func TestDensePrefilterWithNoCandidatesFallsBack(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}

	r := New(Config{
		Vector:       vs,
		Embed:        fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid:       true,
		ANNPrefilter: true,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if vs.queryCalls != 1 {
		t.Fatalf("exhaustive fallback calls = %d, want 1", vs.queryCalls)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("fallback result = %+v, want chunk a", got)
	}
}
