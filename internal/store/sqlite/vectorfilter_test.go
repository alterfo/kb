package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/store/vector"
)

func queryIDs(rs []vector.ScoredChunk) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func TestQueryStructuredFilters(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "c1", RefDocID: "d1", Text: "alpha", Source: "jira", Embedding: []float32{1, 0},
			Metadata: map[string]string{"region": "us-east", "priority": "p0", "last_updated": "2026-05-17", "rps": "800"}},
		{ID: "c2", RefDocID: "d2", Text: "beta", Source: "jira", Embedding: []float32{0.9, 0.1},
			Metadata: map[string]string{"region": "eu-west", "priority": "p0", "last_updated": "2026-05-17", "rps": "800"}},
		{ID: "c3", RefDocID: "d3", Text: "gamma", Source: "slack", Embedding: []float32{0.8, 0.2},
			Metadata: map[string]string{"region": "us-east", "priority": "p2", "last_updated": "2026-05-17", "rps": "800"}},
		{ID: "c4", RefDocID: "d4", Text: "delta", Source: "jira", Embedding: []float32{0.7, 0.3},
			Metadata: map[string]string{"region": "us-east", "priority": "p0", "last_updated": "2025-05-17", "rps": "800"}},
		{ID: "c5", RefDocID: "d5", Text: "epsilon", Source: "jira", Embedding: []float32{0.6, 0.4},
			Metadata: map[string]string{"region": "us-east", "priority": "p0", "last_updated": "2026-05-17", "rps": "100"}},
	})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	filter := vector.Filter{
		Sources:   []string{"jira"},
		Metadata:  map[string]string{"region": "us-east"},
		In:        map[string][]string{"priority": {"p0", "p1"}},
		TimeRange: &vector.TimeRange{Field: "last_updated", From: &from, To: &to},
		Numeric:   []vector.NumericCond{{Field: "rps", Op: vector.OpGt, Value: 500}},
	}

	results, err := vs.Query(ctx, []float32{1, 0}, 10, filter)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results (%v), want 1", len(results), queryIDs(results))
	}
	if results[0].ID != "c1" {
		t.Fatalf("result = %q, want c1", results[0].ID)
	}
}
