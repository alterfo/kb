package qa

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/render"
)

func writeQADoc(t *testing.T, root, rel string, doc connector.Document) {
	t.Helper()
	data, err := render.Render(doc)
	if err != nil {
		t.Fatalf("render.Render: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestBuildGoldenFromRoot_SelectsClosedIssues(t *testing.T) {
	root := t.TempDir()
	writeQADoc(t, root, "leon-ai/closed.md", connector.Document{
		ID: "leon-ai/leon#1", Source: "leon-ai", Kind: "issue", Title: "question one", Body: "expected one",
		Frontmatter: map[string]any{"state": "closed", "number": 1},
	})
	writeQADoc(t, root, "leon-ai/open.md", connector.Document{
		ID: "leon-ai/leon#2", Source: "leon-ai", Kind: "issue", Title: "question two", Body: "expected two",
		Frontmatter: map[string]any{"state": "open", "number": 2},
	})
	writeQADoc(t, root, "leon-ai/pr.md", connector.Document{
		ID: "leon-ai/leon#3", Source: "leon-ai", Kind: "pr", Title: "pr title", Body: "pr body",
		Frontmatter: map[string]any{"state": "closed", "number": 3},
	})
	writeQADoc(t, root, "other/issue.md", connector.Document{
		ID: "other#1", Source: "other", Kind: "issue", Title: "other question", Body: "other expected",
		Frontmatter: map[string]any{"state": "closed", "number": 1},
	})

	got, err := BuildGoldenFromRoot(root, "leon-ai")
	if err != nil {
		t.Fatalf("BuildGoldenFromRoot: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("pairs = %d, want 1", len(got))
	}
	if got[0].ID != "leon-ai/leon#1" || got[0].Question != "question one" || got[0].Expected != "expected one" {
		t.Fatalf("pair = %+v", got[0])
	}
}

func TestWriteAndLoadQAPairs_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qa", "qa_pairs.json")
	in := []QAPair{{ID: "a", Question: "q", Expected: "e"}}
	if err := WriteQAPairs(path, in); err != nil {
		t.Fatalf("WriteQAPairs: %v", err)
	}
	got, err := LoadQAPairs(path)
	if err != nil {
		t.Fatalf("LoadQAPairs: %v", err)
	}
	if len(got) != 1 || got[0].Question != "q" {
		t.Fatalf("pairs = %+v", got)
	}
}

func TestParseQAPairs_RejectsEmptyQuestion(t *testing.T) {
	_, err := ParseQAPairs(strings.NewReader(`[{"question":"","expected":"x"}]`))
	if err == nil {
		t.Fatal("want error for empty question")
	}
}
