package jsonf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExt(t *testing.T) {
	if got := New().Ext(); got != ".json" {
		t.Fatalf("Ext() = %q, want .json", got)
	}
}

func TestImport_NestedFlatten(t *testing.T) {
	docs, err := New().Import("testdata/sample.json")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	d := docs[0]
	if d.Kind != "json" {
		t.Errorf("Kind = %q, want json", d.Kind)
	}
	if d.Title != "sample.json" {
		t.Errorf("Title = %q, want sample.json", d.Title)
	}
	if d.Frontmatter["file_name"] != "sample.json" {
		t.Errorf("frontmatter file_name = %v", d.Frontmatter["file_name"])
	}

	wantLines := []string{
		"- `$.name`: acme-service",
		"- `$.version`: 3",
		"- `$.active`: true",
		"- `$.owner.team`: platform",
		"- `$.owner.contacts[0]`: a@example.com",
		"- `$.owner.contacts[1]`: b@example.com",
		"- `$.notes`: null",
	}
	for _, want := range wantLines {
		if !strings.Contains(d.Body, want) {
			t.Errorf("Body missing line %q, got:\n%s", want, d.Body)
		}
	}
	if strings.Contains(d.Body, "$.tags") {
		t.Errorf("expected no leaf for empty array $.tags, got:\n%s", d.Body)
	}
}

func TestImport_Deterministic(t *testing.T) {
	d1, err := New().Import("testdata/sample.json")
	if err != nil {
		t.Fatalf("Import #1: %v", err)
	}
	d2, err := New().Import("testdata/sample.json")
	if err != nil {
		t.Fatalf("Import #2: %v", err)
	}
	if d1[0].Body != d2[0].Body {
		t.Fatalf("flatten is not deterministic:\n%s\n---\n%s", d1[0].Body, d2[0].Body)
	}
}

func TestImport_MissingFile(t *testing.T) {
	if _, err := New().Import("testdata/does-not-exist.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestImport_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := New().Import(path); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestImport_EmptyObject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	docs, err := New().Import(path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
}
