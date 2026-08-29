package retriever

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestBM25LegRespectsFilter(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "alpha rollout", Source: "slack", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc-b", Text: "alpha rollout", Source: "jira", Embedding: []float32{0, 1}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{Vector: vs, BM25: idx, Hybrid: true})

	got, err := r.Retrieve(context.Background(), "alpha rollout", Options{
		K:      10,
		Filter: vector.Filter{Sources: []string{"jira"}},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "b" {
		t.Fatalf("got %+v, want only chunk b from jira", got)
	}

	all, err := r.Retrieve(context.Background(), "alpha rollout", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve unfiltered: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unfiltered got %d chunks, want 2", len(all))
	}
}

func TestGraphNeighborLegRespectsFilter(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "ga", RefDocID: "doc-ga", Text: "orion project notes", Source: "slack", Embedding: []float32{0, 0}},
		{ID: "gb", RefDocID: "doc-gb", Text: "orion project notes", Source: "confluence", Embedding: []float32{0, 0}},
	}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{
			"orion": {ID: "e-orion", Name: "Orion"},
		},
		neighbors: map[string][]graphstore.Entity{
			"e-orion": {{ID: "e-docs", Name: "Orion Docs", SourceChunks: []string{"ga", "gb"}}},
		},
		relations: map[string][]graphstore.Relation{
			"e-orion": {{Src: "e-orion", Dst: "e-docs", Type: "documented_by", Weight: 1}},
		},
	}

	r := New(Config{Vector: &fakeVectorStore{chunks: chunks}, BM25: idx, Graph: gs})

	got, err := r.Retrieve(context.Background(), "Orion project", Options{
		K:      10,
		Filter: vector.Filter{Sources: []string{"confluence"}},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	for _, sc := range got {
		if sc.Chunk.ID == "ga" {
			t.Fatalf("filtered-out slack chunk ga surfaced: %+v", got)
		}
	}
	foundGB := false
	for _, sc := range got {
		if sc.Chunk.ID == "gb" {
			foundGB = true
		}
	}
	if !foundGB {
		t.Fatalf("confluence chunk gb missing from results: %+v", got)
	}
}
