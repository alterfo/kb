package mcp

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/store/graphstore"
)

func TestGraphQuery_MatchesEntityAndReturnsNeighbors(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()

	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e:alice", Name: "Alice", Type: "person", SourceChunks: []string{"c1"}},
		{ID: "e:bob", Name: "Bob", Type: "person", SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r:1", Src: "e:alice", Dst: "e:bob", Type: "knows", Weight: 1, SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	_, out, err := te.server.graphQuery(ctx, nil, graphQueryIn{Entity: "Alice"})
	if err != nil {
		t.Fatalf("graphQuery: %v", err)
	}
	if len(out.Matched) != 1 || out.Matched[0].ID != "e:alice" {
		t.Fatalf("graphQuery: Matched = %+v", out.Matched)
	}
	if len(out.Neighbors) != 1 || out.Neighbors[0].ID != "e:bob" {
		t.Fatalf("graphQuery: Neighbors = %+v", out.Neighbors)
	}
	if len(out.Relations) != 1 {
		t.Fatalf("graphQuery: Relations = %+v", out.Relations)
	}
}

func TestGraphQuery_UnknownEntityFailsOpenToEmpty(t *testing.T) {
	te := newTestEnv(t, nil)
	_, out, err := te.server.graphQuery(context.Background(), nil, graphQueryIn{Entity: "nobody"})
	if err != nil {
		t.Fatalf("graphQuery: %v", err)
	}
	if len(out.Matched) != 0 {
		t.Fatalf("graphQuery: Matched = %+v, want none", out.Matched)
	}
}

func TestGraphQuery_NilGraphStoreFailsOpen(t *testing.T) {
	te := newTestEnv(t, nil)
	te.server.deps.Graph = nil
	_, out, err := te.server.graphQuery(context.Background(), nil, graphQueryIn{Entity: "Alice"})
	if err != nil {
		t.Fatalf("graphQuery: %v", err)
	}
	if len(out.Matched) != 0 {
		t.Fatalf("graphQuery: Matched = %+v, want none", out.Matched)
	}
}
