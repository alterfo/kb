package mcp

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/store/graphstore"
)

func TestStatus_ReportsChunkAndGraphCounts(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	writeDoc(t, te.root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "hello world"})
	if err := te.indexer.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{{ID: "e:x", Name: "X", Type: "thing", SourceChunks: []string{"c1"}}}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}

	_, out, err := te.server.status(ctx, nil, statusIn{})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if out.ChunkCount != 1 {
		t.Fatalf("status: ChunkCount = %d, want 1", out.ChunkCount)
	}
	if out.EntityCount != 1 {
		t.Fatalf("status: EntityCount = %d, want 1", out.EntityCount)
	}
	if out.CorpusVersion <= 0 {
		t.Fatalf("status: CorpusVersion = %d, want >0 after a write", out.CorpusVersion)
	}
}

func TestStatus_EmptyCorpusReturnsZeros(t *testing.T) {
	te := newTestEnv(t, nil)
	_, out, err := te.server.status(context.Background(), nil, statusIn{})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if out.ChunkCount != 0 || out.EntityCount != 0 || out.RelationCount != 0 || out.CommunityCount != 0 {
		t.Fatalf("status: out = %+v, want all zero", out)
	}
}
