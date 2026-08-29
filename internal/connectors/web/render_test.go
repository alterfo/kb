package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/render"
)

func TestBuildDocument_FrontmatterGolden(t *testing.T) {
	d := buildDocument("leon-docs", "https://docs.getleon.ai/docs/getting-started", "Getting Started", "# Getting Started\n\nLeon is an open-source assistant.")
	if d.Kind != "doc_page" {
		t.Fatalf("Kind = %q, want doc_page", d.Kind)
	}
	if d.ID != "docs/getting-started" {
		t.Fatalf("ID = %q", d.ID)
	}
	if d.Frontmatter["path"] != "/docs/getting-started" {
		t.Fatalf("path = %v", d.Frontmatter["path"])
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "doc_page.md"))
	if err != nil {
		t.Fatalf("reading golden doc_page.md: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("render mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuildDocument_RootPathID(t *testing.T) {
	d := buildDocument("src", "https://docs.getleon.ai/", "", "")
	if d.ID != "index" {
		t.Fatalf("ID = %q, want index", d.ID)
	}
}

func TestBuildDocument_TitleFallback(t *testing.T) {
	d := buildDocument("src", "https://docs.getleon.ai/docs/intro", "", "")
	if d.Title != "/docs/intro" {
		t.Fatalf("Title = %q, want path fallback", d.Title)
	}
}

func TestBuildDocument_QueryStringDistinctID(t *testing.T) {
	a := buildDocument("src", "https://docs.getleon.ai/list?page=1", "A", "body")
	b := buildDocument("src", "https://docs.getleon.ai/list?page=2", "B", "body")
	if a.ID == b.ID {
		t.Fatalf("IDs collide: %q", a.ID)
	}
	if !strings.HasPrefix(a.ID, "list-") {
		t.Errorf("ID = %q, want list- prefix", a.ID)
	}
}
