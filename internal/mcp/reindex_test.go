package mcp

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

func TestReindex_SinglePathIndexesAndInvalidatesBM25(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	writeDoc(t, te.root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "unique-reindex-token content"})

	_, out, err := te.server.reindex(ctx, nil, reindexIn{Path: "notes/doc1.md"})
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if out.Indexed != 1 {
		t.Fatalf("reindex: Indexed = %d, want 1", out.Indexed)
	}

	_, sout, err := te.server.search(ctx, nil, searchIn{Query: "unique-reindex-token"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(sout.Results) == 0 {
		t.Fatalf("search: reindexed document not found")
	}
}

func TestReindex_EmptyPathBuildsWholeTree(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	writeDoc(t, te.root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "a"})
	writeDoc(t, te.root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "b"})

	_, out, err := te.server.reindex(ctx, nil, reindexIn{})
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if out.Indexed != 2 {
		t.Fatalf("reindex: Indexed = %d, want 2", out.Indexed)
	}
}

func TestReindex_NoIndexerConfiguredReturnsError(t *testing.T) {
	te := newTestEnv(t, nil)
	te.server.deps.Indexer = nil
	if _, _, err := te.server.reindex(context.Background(), nil, reindexIn{}); err == nil {
		t.Fatalf("reindex: got nil error with no indexer configured, want error")
	}
}
