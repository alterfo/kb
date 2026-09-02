package sqlite

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestIndexStatsCountsIndexContents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}
	if err := vs.Upsert(ctx, []vector.Chunk{
		{ID: "c1", RefDocID: "doc1", Text: "alpha", FilePath: "notes/a.md", FileName: "a.md", Source: "notes", Embedding: []float32{1, 0}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	gs := NewGraphStore(db)
	if err := gs.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e1", Name: "KB", Type: "project", SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := gs.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r1", Src: "e1", Dst: "e1", Type: "relates", Weight: 1, SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	if err := gs.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "comm1", Level: 0, Members: []string{"e1"}, Summary: "s", Title: "t"},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	stats, err := db.IndexStats(ctx)
	if err != nil {
		t.Fatalf("IndexStats: %v", err)
	}
	if !stats.HasEmbedDim || stats.EmbedDim != 2 {
		t.Fatalf("EmbedDim = (%d,%v), want (2,true)", stats.EmbedDim, stats.HasEmbedDim)
	}
	if stats.Chunks != 1 || stats.EmbeddedChunks != 1 {
		t.Fatalf("chunk counts = (%d,%d), want (1,1)", stats.Chunks, stats.EmbeddedChunks)
	}
	if stats.Entities != 1 || stats.Relations != 1 || stats.Communities != 1 {
		t.Fatalf("graph counts = (%d,%d,%d), want (1,1,1)", stats.Entities, stats.Relations, stats.Communities)
	}
}
