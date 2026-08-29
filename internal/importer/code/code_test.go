package code

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportGoFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "demo.go")
	src := "package demo\n\nfunc Add(a, b int) int { return a + b }\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	docs, err := New().Import(p)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	d := docs[0]
	if d.Kind != "code" {
		t.Errorf("Kind = %q, want code", d.Kind)
	}
	if d.Body != src {
		t.Errorf("Body = %q, want %q", d.Body, src)
	}
	if d.ID != p {
		t.Errorf("ID = %q, want %q", d.ID, p)
	}
	if d.Title != "demo.go" {
		t.Errorf("Title = %q, want demo.go", d.Title)
	}
	if d.Frontmatter["file_name"] != "demo.go" {
		t.Errorf("frontmatter file_name = %v, want demo.go", d.Frontmatter["file_name"])
	}
}

func TestExt(t *testing.T) {
	if got := New().Ext(); got != ".go" {
		t.Fatalf("Ext() = %q, want .go", got)
	}
}

func TestImportMissingFile(t *testing.T) {
	_, err := New().Import(filepath.Join(t.TempDir(), "nope.go"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
