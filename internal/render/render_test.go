package render

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
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

func TestRenderFullDocumentGolden(t *testing.T) {
	d := connector.Document{
		ID:         "42",
		Source:     "github-myorg",
		Kind:       "issue",
		Title:      "Fix the thing",
		URL:        "https://github.com/myorg/repo/issues/42",
		UpdatedAt:  time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		Body:       "Some body text.\n\nSecond paragraph.",
		Visibility: "public",
		Frontmatter: map[string]any{
			"repo":   "myorg/repo",
			"state":  "open",
			"labels": []string{"bug", "priority:high"},
			"author": "octocat",
		},
	}

	got, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "full_document.md", got)
}

func TestRenderMinimalDocumentGolden(t *testing.T) {
	d := connector.Document{
		ID:     "page-1",
		Source: "wiki-docs",
		Title:  "",
		Body:   "Just a body.",
	}

	got, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "minimal_document.md", got)
}

func TestRenderEditAtFrontmatterGolden(t *testing.T) {
	d := connector.Document{
		ID:         "slack:C1:1700000010.000100",
		Source:     "main-workspace",
		Kind:       "message",
		Title:      "Deploy finished successfully.",
		URL:        "https://slack.com/archives/C1/p1700000010000100",
		UpdatedAt:  time.Date(2023, 11, 14, 22, 13, 30, 0, time.UTC),
		Body:       "Deploy finished successfully.",
		Visibility: "public",
		Frontmatter: map[string]any{
			"channel": "C1",
			"ts":      "1700000010.000100",
			"user":    "U1",
			"edit_at": "2023-11-14T22:13:35Z",
		},
	}

	got, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "edit_at.md", got)
}

func TestRenderEscapesSpecialCharacters(t *testing.T) {
	d := connector.Document{
		ID:     "esc-1",
		Source: "wiki-docs",
		Title:  `Title: with a colon, "quotes" and a trailing space `,
		Body:   "Body with # a hash and: a colon.",
		Frontmatter: map[string]any{
			"note": "multi\nline\nvalue",
		},
	}

	got, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "escaping.md", got)
}

func TestRenderStableKeyOrderIgnoresFrontmatterInsertionOrder(t *testing.T) {
	base := connector.Document{
		ID:     "order-1",
		Source: "wiki-docs",
		Title:  "Order test",
		Body:   "body",
		Frontmatter: map[string]any{
			"zeta":  "z",
			"alpha": "a",
			"mu":    "m",
		},
	}

	got1, err := Render(base)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got2, err := Render(base)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(got1) != string(got2) {
		t.Fatalf("Render is not deterministic across calls")
	}
	compareGolden(t, "sorted_frontmatter.md", got1)
}

func TestRenderReservedKeyInFrontmatterIsIgnored(t *testing.T) {
	d := connector.Document{
		ID:     "reserved-1",
		Source: "wiki-docs",
		Title:  "Reserved test",
		Body:   "body",
		Frontmatter: map[string]any{
			"id":     "should-not-override",
			"custom": "kept",
		},
	}

	got, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "reserved_key.md", got)
}

func TestRenderSummaryGolden(t *testing.T) {
	d := connector.Document{
		ID:        "summary-1",
		Source:    "wiki-docs",
		Kind:      "page",
		Title:     "Summary title",
		UpdatedAt: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		Body:      "Full document body.\n\nSecond line.",
		Summary:   "A concise summary.",
		Frontmatter: map[string]any{
			"tags": []string{"one", "two"},
		},
	}

	got, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "summary.md", got)
}

func TestRenderTrimsTrailingNewlinesInBody(t *testing.T) {
	d := connector.Document{
		ID:     "trim-1",
		Source: "wiki-docs",
		Title:  "Trim test",
		Body:   "line one\nline two\n\n\n",
	}

	got, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "trim_body.md", got)
}

func TestParseRoundTripsFullDocument(t *testing.T) {
	d := connector.Document{
		ID:         "42",
		Source:     "github-myorg",
		Kind:       "issue",
		Title:      "Fix the thing",
		URL:        "https://github.com/myorg/repo/issues/42",
		UpdatedAt:  time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		Body:       "Some body text.\n\nSecond paragraph.",
		Visibility: "public",
	}

	rendered, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.ID != d.ID || got.Source != d.Source || got.Kind != d.Kind || got.Title != d.Title ||
		got.URL != d.URL || got.Visibility != d.Visibility || got.Body != d.Body {
		t.Fatalf("Parse round-trip mismatch: got %+v, want %+v", got, d)
	}
	if !got.UpdatedAt.Equal(d.UpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, d.UpdatedAt)
	}
}

func TestParseLiftsSummaryIntoField(t *testing.T) {
	d := connector.Document{
		ID:      "summary-2",
		Source:  "wiki-docs",
		Title:   "T",
		Body:    "body",
		Summary: "typed summary",
	}

	rendered, err := Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Summary != "typed summary" {
		t.Fatalf("Summary = %q, want typed summary", got.Summary)
	}
	if _, ok := got.Frontmatter["summary"]; ok {
		t.Fatalf("summary leaked into Frontmatter: %+v", got.Frontmatter)
	}
}

func TestParseKeepsExtraFrontmatterKeys(t *testing.T) {
	data, err := os.ReadFile(goldenPath("full_document.md"))
	if err != nil {
		t.Fatalf("reading golden: %v", err)
	}
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Frontmatter["repo"] != "myorg/repo" || got.Frontmatter["state"] != "open" || got.Frontmatter["author"] != "octocat" {
		t.Fatalf("unexpected frontmatter: %+v", got.Frontmatter)
	}
	if _, ok := got.Frontmatter["id"]; ok {
		t.Fatalf("reserved key %q leaked into Frontmatter", "id")
	}
}

func TestParseMissingFrontmatterDelimiterErrors(t *testing.T) {
	if _, err := Parse([]byte("just a body, no frontmatter")); err == nil {
		t.Fatal("expected error for missing frontmatter delimiter")
	}
}

func TestParseUnterminatedFrontmatterErrors(t *testing.T) {
	if _, err := Parse([]byte("---\nid: a\n")); err == nil {
		t.Fatal("expected error for unterminated frontmatter")
	}
}
