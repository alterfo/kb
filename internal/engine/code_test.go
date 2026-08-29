package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/store/sqlite"
)

func TestAddOrUpdateDocumentIndexesCodeAsSingleChunk(t *testing.T) {
	root := t.TempDir()
	src := `package calc

func Add(a, b int) int {
	return a + b
}
`
	writeDoc(t, root, "code/calc.go", connector.Document{
		ID:     "code/calc.go",
		Source: "code",
		Kind:   "code",
		Title:  "calc.go",
		Body:   src,
		Frontmatter: map[string]any{
			"file_path": "code/calc.go",
			"file_name": "calc.go",
		},
	})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	graphStore := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(graphStore, nil, nil)
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 64})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "code/calc.go"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1 (whole file as one chunk)", len(chunks))
	}
	c := chunks[0]
	if c.Text != strings.TrimRight(src, "\n") {
		t.Errorf("chunk text = %q, want %q", c.Text, strings.TrimRight(src, "\n"))
	}
	if c.Metadata["kind"] != "code" {
		t.Errorf("chunk kind metadata = %q, want code", c.Metadata["kind"])
	}

	entities, err := graphStore.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	relations, err := graphStore.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(entities) == 0 || len(relations) == 0 {
		t.Fatalf("expected code graph after indexing, got %d entities, %d relations", len(entities), len(relations))
	}
}
