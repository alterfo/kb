package sqlite

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/store/vector"
)

func TestQueryCandidatesScoresOnlyCandidateSet(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}
	mustUpsert(t, vs, []vector.Chunk{
		{ID: "c1", RefDocID: "d1", Text: "apple", Embedding: []float32{1, 0}},
		{ID: "c2", RefDocID: "d2", Text: "banana", Embedding: []float32{0, 1}},
		{ID: "c3", RefDocID: "d3", Text: "apple pie", Embedding: []float32{0.9, 0.1}},
	})

	got, err := vs.QueryCandidates(ctx, []float32{1, 0}, 10, []string{"c1", "c2", "c1"}, vector.Filter{})
	if err != nil {
		t.Fatalf("QueryCandidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results (%v), want 2 candidate rows", len(got), queryIDs(got))
	}
	if got[0].ID != "c1" || got[1].ID != "c2" {
		t.Fatalf("results = %v, want [c1 c2]", queryIDs(got))
	}
}

func TestQueryCandidatesAppliesFilterAndEmptySet(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}
	mustUpsert(t, vs, []vector.Chunk{
		{ID: "c1", RefDocID: "d1", Text: "apple", Source: "jira", Embedding: []float32{1, 0}},
		{ID: "c2", RefDocID: "d2", Text: "banana", Source: "slack", Embedding: []float32{0, 1}},
	})

	got, err := vs.QueryCandidates(ctx, []float32{1, 0}, 10, []string{"c1", "c2"}, vector.Filter{Sources: []string{"jira"}})
	if err != nil {
		t.Fatalf("QueryCandidates: %v", err)
	}
	if len(got) != 1 || got[0].ID != "c1" {
		t.Fatalf("results = %v, want only c1 after source filter", queryIDs(got))
	}

	empty, err := vs.QueryCandidates(ctx, []float32{1, 0}, 10, nil, vector.Filter{})
	if err != nil {
		t.Fatalf("QueryCandidates(empty): %v", err)
	}
	if empty != nil {
		t.Fatalf("QueryCandidates(empty) = %v, want nil", empty)
	}
}
