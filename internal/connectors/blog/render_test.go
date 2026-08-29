package blog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/render"
)

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading golden %s: %v", name, err)
	}
	if string(got) != string(want) {
		t.Fatalf("render mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestBuildDocument_FrontmatterGolden(t *testing.T) {
	item := rssItem{
		Title:       "Leon 2.0 is here",
		Link:        "https://blog.getleon.ai/leon-2-0",
		GUID:        "https://blog.getleon.ai/?p=42",
		PubDate:     "Sat, 22 Aug 2026 10:30:00 +0000",
		Description: "<p>Short summary.</p>",
		Content:     "<h1>Leon 2.0</h1><p>Full <strong>post</strong> body.</p>",
	}

	d := buildDocument("leon-blog", item)
	if d.Kind != "blog_post" {
		t.Fatalf("Kind = %q, want blog_post", d.Kind)
	}
	if d.ID != "https://blog.getleon.ai/?p=42" {
		t.Fatalf("ID = %q", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "blog_post.md", got)
}

func TestBuildDocument_GUIDFallbackToLink(t *testing.T) {
	item := rssItem{Title: "No guid", Link: "https://example.com/post"}
	d := buildDocument("src", item)
	if d.ID != "https://example.com/post" {
		t.Fatalf("ID = %q, want link fallback", d.ID)
	}
	if _, ok := d.Frontmatter["guid"]; ok {
		t.Fatal("guid should be absent when missing")
	}
}

func TestBuildDocument_ContentPreferredOverDescription(t *testing.T) {
	item := rssItem{Description: "fallback", Content: "full content"}
	d := buildDocument("src", item)
	if d.Body != "full content" {
		t.Fatalf("Body = %q, want content", d.Body)
	}
}

func TestParsePubDate(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"Sat, 22 Aug 2026 10:30:00 +0000", time.Date(2026, 8, 22, 10, 30, 0, 0, time.UTC)},
		{"Fri, 21 Aug 2026 09:00:00 GMT", time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)},
		{"", time.Time{}},
		{"not a date", time.Time{}},
	}
	for _, tc := range cases {
		got := parsePubDate(tc.in)
		if !got.Equal(tc.want) {
			t.Errorf("parsePubDate(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
