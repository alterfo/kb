package markdown

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExt(t *testing.T) {
	if got := New().Ext(); got != ".md" {
		t.Fatalf("Ext() = %q, want .md", got)
	}
}

func TestImport_PlainMarkdown(t *testing.T) {
	docs, err := New().Import("testdata/sample.md")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	d := docs[0]
	if d.Kind != "md" {
		t.Errorf("Kind = %q, want md", d.Kind)
	}
	if d.Title != "Sample Project Plan" {
		t.Errorf("Title = %q, want frontmatter title", d.Title)
	}
	if d.Frontmatter["status"] != "active" {
		t.Errorf("frontmatter status = %v, want active", d.Frontmatter["status"])
	}
	if d.Frontmatter["file_name"] != "sample.md" {
		t.Errorf("frontmatter file_name = %v", d.Frontmatter["file_name"])
	}
	if strings.Contains(d.Body, "---") {
		t.Errorf("body must not contain the frontmatter block:\n%s", d.Body)
	}
	if !strings.Contains(d.Body, "Section One") {
		t.Errorf("body missing content:\n%s", d.Body)
	}
}

func TestImport_TitleFromHeading(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	body := "# Hello World\n\nSome text."
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	docs, err := New().Import(path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Title != "Hello World" {
		t.Errorf("Title = %q, want first heading", docs[0].Title)
	}
	if docs[0].Body != body {
		t.Errorf("Body must pass through unchanged for files without frontmatter")
	}
}

func TestImport_LegalDelegation(t *testing.T) {
	docs, err := New().Import("testdata/legal.md")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 legal document, got %d", len(docs))
	}
	if docs[0].Kind != "legal-article" {
		t.Errorf("Kind = %q, want legal-article", docs[0].Kind)
	}
	if docs[0].Frontmatter["code"] != "гк-рф" {
		t.Errorf("frontmatter code = %v, want гк-рф", docs[0].Frontmatter["code"])
	}
}

func TestImport_MissingFile(t *testing.T) {
	if _, err := New().Import("testdata/does-not-exist.md"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestImport_Deterministic(t *testing.T) {
	d1, err := New().Import("testdata/sample.md")
	if err != nil {
		t.Fatalf("Import #1: %v", err)
	}
	d2, err := New().Import("testdata/sample.md")
	if err != nil {
		t.Fatalf("Import #2: %v", err)
	}
	if d1[0].Body != d2[0].Body || d1[0].Title != d2[0].Title {
		t.Fatalf("import is not deterministic:\n%#v\n---\n%#v", d1[0], d2[0])
	}
}
