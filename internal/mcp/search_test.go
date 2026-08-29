package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

func TestSearch_ReturnsIndexedChunk(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	writeDoc(t, te.root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "The rain in Spain falls mainly on the plain."})
	if err := te.indexer.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	_, out, err := te.server.search(ctx, nil, searchIn{Query: "rain Spain"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out.Results) == 0 {
		t.Fatalf("search: got 0 results, want >=1")
	}
	if !strings.Contains(out.Results[0].Text, "rain") {
		t.Fatalf("search: result text = %q, want it to contain %q", out.Results[0].Text, "rain")
	}
	if out.Results[0].FilePath != "notes/doc1.md" {
		t.Fatalf("search: FilePath = %q, want %q", out.Results[0].FilePath, "notes/doc1.md")
	}
}

func TestSearch_SourceFilterExcludesOtherSources(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	writeDoc(t, te.root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "unique-alpha-token content"})
	writeDoc(t, te.root, "github/b.md", connector.Document{ID: "b", Source: "github", Body: "unique-alpha-token content"})
	if err := te.indexer.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}
	if err := te.indexer.AddOrUpdateDocument(ctx, "github/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	_, out, err := te.server.search(ctx, nil, searchIn{Query: "unique-alpha-token", Source: "notes"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].Source != "notes" {
		t.Fatalf("search: results = %+v, want exactly 1 result from source=notes", out.Results)
	}
}

func TestSearch_NoResultsOnEmptyCorpus(t *testing.T) {
	te := newTestEnv(t, nil)
	_, out, err := te.server.search(context.Background(), nil, searchIn{Query: "anything"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out.Results) != 0 {
		t.Fatalf("search: got %d results on empty corpus, want 0", len(out.Results))
	}
}

func TestSearch_FiltersByVirtualCollection(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	sourcesYAML := "sources:\n  - name: tg\n    type: telegram\n  - name: notes\n    type: file\nvirtual_collections:\n  chats: [telegram:*]\n"
	if err := os.WriteFile(filepath.Join(te.root, "sources.yaml"), []byte(sourcesYAML), 0o644); err != nil {
		t.Fatalf("writing sources.yaml: %v", err)
	}
	writeDoc(t, te.root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "unique-chat-token content"})
	writeDoc(t, te.root, "telegram/b.md", connector.Document{ID: "b", Source: "tg", Body: "unique-chat-token content"})
	if err := te.indexer.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}
	if err := te.indexer.AddOrUpdateDocument(ctx, "telegram/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	_, out, err := te.server.search(ctx, nil, searchIn{Query: "unique-chat-token", Source: "chats"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].Source != "tg" {
		t.Fatalf("search: results = %+v, want exactly 1 result from virtual collection chats", out.Results)
	}
}
