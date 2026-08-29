package web

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/markdown"
)

func TestHTMLToMarkdown_Golden(t *testing.T) {
	root, err := parseHTML(fixture(t, "page.html"))
	if err != nil {
		t.Fatalf("parseHTML: %v", err)
	}
	content := extractContent(root, "main")
	base, _ := url.Parse("https://docs.getleon.ai/docs/getting-started")
	got := markdown.Render(content, base)

	want, err := os.ReadFile(filepath.Join("testdata", "page.md"))
	if err != nil {
		t.Fatalf("reading golden page.md: %v", err)
	}
	wantStr := strings.TrimRight(string(want), "\n")
	if got != wantStr {
		t.Fatalf("markdown mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, wantStr)
	}
}

func TestParseSitemap_ResolvesRelativeURLs(t *testing.T) {
	pages, children, err := parseSitemap(fixture(t, "sitemap.xml"), "https://docs.getleon.ai/sitemap.xml")
	if err != nil {
		t.Fatalf("parseSitemap: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("children = %v, want none", children)
	}
	want := []string{
		"https://docs.getleon.ai/docs/getting-started",
		"https://docs.getleon.ai/docs/installation",
		"https://docs.getleon.ai/docs/advanced",
	}
	if len(pages) != len(want) {
		t.Fatalf("pages = %v, want %v", pages, want)
	}
	for i := range want {
		if pages[i] != want[i] {
			t.Errorf("pages[%d] = %q, want %q", i, pages[i], want[i])
		}
	}
}

func TestParseSitemap_InvalidXMLIsError(t *testing.T) {
	_, _, err := parseSitemap([]byte(`<urlset><url><loc>`), "https://docs.getleon.ai/sitemap.xml")
	if err == nil {
		t.Fatal("expected error on invalid sitemap XML")
	}
}

func TestExtractTitle_PrefersTitleOverH1(t *testing.T) {
	root, err := parseHTML(fixture(t, "page.html"))
	if err != nil {
		t.Fatalf("parseHTML: %v", err)
	}
	if got := extractTitle(root); got != "Getting Started" {
		t.Errorf("extractTitle = %q, want Getting Started", got)
	}
}

func TestExtractTitle_FallsBackToH1(t *testing.T) {
	root, err := parseHTML([]byte(`<html><body><h1>Only Heading</h1></body></html>`))
	if err != nil {
		t.Fatalf("parseHTML: %v", err)
	}
	if got := extractTitle(root); got != "Only Heading" {
		t.Errorf("extractTitle = %q, want Only Heading", got)
	}
}

func TestExtractContent_FallbackToBody(t *testing.T) {
	root, err := parseHTML(fixture(t, "no-main.html"))
	if err != nil {
		t.Fatalf("parseHTML: %v", err)
	}
	content := extractContent(root, "main")
	if content == nil {
		t.Fatal("extractContent returned nil")
	}
	if content.Data != "body" {
		t.Errorf("extractContent tag = %q, want body", content.Data)
	}
}
