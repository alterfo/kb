package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

func fakeEnv(m map[string]string) connector.EnvLookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestType(t *testing.T) {
	if got := New().Type(); got != "file" {
		t.Fatalf("Type() = %q, want file", got)
	}
}

func TestResolve_RequiresPath(t *testing.T) {
	c := New()
	err := c.Resolve(context.Background(), connector.Config{Name: "docs"}, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when config.path is missing")
	}
}

func drain(out chan connector.Document) (*[]connector.Document, <-chan struct{}) {
	docs := &[]connector.Document{}
	done := make(chan struct{})
	go func() {
		for d := range out {
			*docs = append(*docs, d)
		}
		close(done)
	}()
	return docs, done
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestFetch_DispatchesByExtensionAndSetsSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.json"), `{"a":1}`)
	writeFile(t, filepath.Join(dir, "schema.sql"), "CREATE TABLE t (id INTEGER PRIMARY KEY);")

	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "localdocs", Config: map[string]string{"path": dir}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("expected 2 docs, got info=%+v docs=%d", info, len(*docs))
	}
	if !info.FullReconcile {
		t.Error("expected FullReconcile = true for local file scans")
	}
	for _, d := range *docs {
		if d.Source != "localdocs" {
			t.Errorf("Source = %q, want localdocs", d.Source)
		}
	}
}

func TestFetch_RecursesSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.json"), `{"x":1}`)
	writeFile(t, filepath.Join(dir, "nested", "b.json"), `{"y":2}`)

	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "docs", Config: map[string]string{"path": dir}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("expected 2 docs across subdirectories, got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_SkipsUnsupportedExtensions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.txt"), "plain text, not importable")
	writeFile(t, filepath.Join(dir, "data.json"), `{"ok":true}`)

	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "docs", Config: map[string]string{"path": dir}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 1 || len(*docs) != 1 {
		t.Fatalf("expected 1 doc (unsupported .txt skipped), got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_BrokenFileDefersPrune(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "good.json"), `{"a":1}`)
	writeFile(t, filepath.Join(dir, "broken.json"), `{invalid json`)

	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "docs", Config: map[string]string{"path": dir}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1 (broken file skipped fail-open)", len(*docs))
	}
	if info.FullReconcile {
		t.Fatal("FullReconcile must be false when an importable file failed, so prune does not delete its previous docs")
	}
}
func TestFetch_VisibilityAppliedWhenUnset(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "data.json"), `{"ok":true}`)

	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "docs", Config: map[string]string{
		"path":       dir,
		"visibility": "internal",
	}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 1 || (*docs)[0].Visibility != "internal" {
		t.Fatalf("docs = %+v, want visibility=internal", *docs)
	}
}

func TestFetch_MultiDocumentImporterProducesOneDocPerTable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "schema.sql"), `
CREATE TABLE users (id INTEGER PRIMARY KEY);
CREATE TABLE posts (id INTEGER PRIMARY KEY, author_id INTEGER REFERENCES users(id));
`)

	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "docs", Config: map[string]string{"path": dir}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("expected 2 docs (one per table), got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_NonexistentRootErrors(t *testing.T) {
	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "docs", Config: map[string]string{"path": "/does/not/exist"}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error when root directory does not exist")
	}
}

func TestFetch_ImportsGoFilesWithKindCode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "calc.go"), "package calc\n\nfunc Add(a, b int) int { return a + b }\n")

	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "code", Config: map[string]string{"path": dir}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 1 || len(*docs) != 1 {
		t.Fatalf("expected 1 doc, got info=%+v docs=%d", info, len(*docs))
	}
	d := (*docs)[0]
	if d.Kind != "code" {
		t.Errorf("Kind = %q, want code", d.Kind)
	}
	if d.Body != "package calc\n\nfunc Add(a, b int) int { return a + b }\n" {
		t.Errorf("unexpected body: %q", d.Body)
	}
}

func TestFetch_MarkdownRoutedToLegalruImporter(t *testing.T) {
	dir := t.TempDir()
	// The .md extension is handled by the markdown importer, which
	// delegates to the legalru parser for curated codex corpus files and
	// falls back to plain-markdown import for everything else.
	writeFile(t, filepath.Join(dir, "note.md"), "# Заметка\n\nОбычный текст.\n")
	writeFile(t, filepath.Join(dir, "codex.md"), `# [гк-рф] Гражданский кодекс Российской Федерации

# Часть первая

## Раздел I. Общие положения

### Глава 1. Гражданское законодательство

#### Статья 1. Основные начала гражданского законодательства

1. Гражданское законодательство основывается на признании равенства участников регулируемых им отношений.
`)

	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "docs", Config: map[string]string{"path": dir}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// The legal codex article is imported by the legalru parser, the plain
	// note by the markdown fallback.
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("expected 2 docs, got info=%+v docs=%d", info, len(*docs))
	}
	kinds := map[string]bool{}
	for _, d := range *docs {
		kinds[d.Kind] = true
	}
	if !kinds["legal-article"] || !kinds["md"] {
		t.Errorf("expected kinds legal-article and md, got %v", kinds)
	}
}
