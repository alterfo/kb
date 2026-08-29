package wiki

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/render"
)

func goldenPath(name string) string {
	return filepath.Join("testdata", name)
}

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(goldenPath(name))
	if err != nil {
		t.Fatalf("reading golden %s: %v", name, err)
	}
	if string(got) != string(want) {
		t.Fatalf("render mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestBuildMediaWikiDocument_FrontmatterGolden(t *testing.T) {
	rc := apiRecentChange{
		Type:      "edit",
		Ns:        0,
		Title:     "Setup Guide",
		PageID:    42,
		RevID:     7,
		Timestamp: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
	}

	d := buildMediaWikiDocument("public-wiki", "en.wikipedia.org", "https://en.wikipedia.org", rc, "== Setup ==\n\nSteps here.")
	if d.Kind != "page" {
		t.Fatalf("Kind = %q, want page", d.Kind)
	}
	if d.ID != "en.wikipedia.org:42" {
		t.Fatalf("ID = %q, want en.wikipedia.org:42", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "mediawiki_page.md", got)
}

func TestBuildConfluenceDocument_FrontmatterGolden(t *testing.T) {
	res := apiConfluenceContent{
		ID:    "55",
		Title: "Setup Guide",
	}
	res.Space.Key = "ENG"
	res.Version.Number = 4
	res.Version.When = time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	res.Ancestors = []struct {
		Title string `json:"title"`
	}{{Title: "Root"}, {Title: "Docs"}}
	res.Body.Storage.Value = "<p>Steps here.</p>"
	res.Links.WebUI = "/spaces/ENG/pages/55"

	d := buildConfluenceDocument("corp-wiki", "https://acme.atlassian.net/wiki", res)
	if d.Kind != "page" {
		t.Fatalf("Kind = %q, want page", d.Kind)
	}
	if d.ID != "confluence:ENG:55" {
		t.Fatalf("ID = %q, want confluence:ENG:55", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "confluence_page.md", got)
}

func TestBuildConfluenceDocument_NoAncestorsOmitsField(t *testing.T) {
	res := apiConfluenceContent{ID: "1", Title: "Home"}
	res.Space.Key = "ENG"
	d := buildConfluenceDocument("corp-wiki", "https://acme.atlassian.net/wiki", res)
	if _, ok := d.Frontmatter["ancestors"]; ok {
		t.Fatalf("Frontmatter[ancestors] should be absent when no ancestors, got %v", d.Frontmatter["ancestors"])
	}
}
