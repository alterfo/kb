package engine

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
)

func walkMarkdownPaths(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	return paths, err
}

func hasEntityName(entities []graphstore.Entity, name string) bool {
	for _, e := range entities {
		if e.Name == name {
			return true
		}
	}
	return false
}

func TestCorpusWalkNoDemoDocsAfterRemoval(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"demo-docs/decisions.md", "leon-ai/issue.md"} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("body"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	if err := os.RemoveAll(filepath.Join(root, "demo-docs")); err != nil {
		t.Fatalf("RemoveAll demo-docs: %v", err)
	}

	paths, err := walkMarkdownPaths(root)
	if err != nil {
		t.Fatalf("walkMarkdownPaths: %v", err)
	}
	for _, p := range paths {
		if p == "demo-docs" || strings.HasPrefix(p, "demo-docs/") {
			t.Errorf("demo-docs file still present in corpus: %s", p)
		}
	}
	foundLeon := false
	for _, p := range paths {
		if p == "leon-ai/issue.md" {
			foundLeon = true
		}
	}
	if !foundLeon {
		t.Errorf("leon-ai/issue.md missing after demo cleanup, got %v", paths)
	}
}

const (
	demoExtractionJSON = `{"entities":[{"name":"oleg","type":"person"},{"name":"sqlite-vec","type":"extension"}],"relations":[{"source":"oleg","target":"sqlite-vec","type":"uses"}]}`
	leonExtractionJSON = `{"entities":[{"name":"Leon","type":"project"}],"relations":[]}`
)

func TestReindexPurgesDemoEntitiesFromGraphStore(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "demo-docs/decisions.md", connector.Document{ID: "decisions", Source: "demo-docs", Body: "Demo decisions about sqlite-vec and oleg."})
	writeDoc(t, root, "leon-ai/issue.md", connector.Document{ID: "issue", Source: "leon-ai", Body: "Leon project issue."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	chat := &scriptedChat{responses: []string{demoExtractionJSON, leonExtractionJSON}}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), nil)
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	if _, err := ix.Reindex(ctx, ""); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	entities, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if !hasEntityName(entities, "oleg") || !hasEntityName(entities, "sqlite-vec") || !hasEntityName(entities, "Leon") {
		t.Fatalf("expected demo and leon entities after initial index, got %+v", entities)
	}

	if err := os.RemoveAll(filepath.Join(root, "demo-docs")); err != nil {
		t.Fatalf("RemoveAll demo-docs: %v", err)
	}

	res, err := ix.Reindex(ctx, "")
	if err != nil {
		t.Fatalf("Reindex after removal: %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("Reindex result = %+v, want Removed=1", res)
	}

	entities, err = gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities after removal: %v", err)
	}
	if hasEntityName(entities, "oleg") || hasEntityName(entities, "sqlite-vec") {
		t.Fatalf("demo entities remain after reindex: %+v", entities)
	}
	if !hasEntityName(entities, "Leon") {
		t.Fatalf("leon entity lost after reindex: %+v", entities)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	for _, c := range chunks {
		if c.Source == "demo-docs" {
			t.Errorf("demo-docs chunk still indexed: %+v", c)
		}
	}
}
