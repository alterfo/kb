package searchapi

import (
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestBuildDocument_FieldMapMatrix(t *testing.T) {
	raw := `{
		"identifier": "doc-42",
		"heading": "Invoice 42",
		"link": "https://example.com/doc-42",
		"modified": "2026-03-05T10:00:00Z",
		"content": "Full invoice body",
		"meta": {"author": {"name": "Ada"}, "state": "open"}
	}`
	item := gjson.Parse(raw)

	fm := fieldMap{
		ID:        "identifier",
		Title:     "heading",
		URL:       "link",
		UpdatedAt: "modified",
		Body:      "content",
		Extra: map[string]string{
			"author": "meta.author.name",
			"status": "meta.state",
		},
	}

	doc, updated, ok := buildDocument("sa", "result", "internal", fm, time.RFC3339, item)
	if !ok {
		t.Fatal("expected buildDocument to succeed")
	}
	if doc.ID != "doc-42" || doc.Title != "Invoice 42" || doc.URL != "https://example.com/doc-42" ||
		doc.Body != "Full invoice body" || doc.Source != "sa" || doc.Kind != "result" || doc.Visibility != "internal" {
		t.Errorf("doc = %+v", doc)
	}
	want := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	if !updated.Equal(want) || !doc.UpdatedAt.Equal(want) {
		t.Errorf("updated = %v, want %v", updated, want)
	}
	if doc.Frontmatter["author"] != "Ada" || doc.Frontmatter["status"] != "open" {
		t.Errorf("frontmatter = %+v", doc.Frontmatter)
	}
}

func TestBuildDocument_MissingIDSkipped(t *testing.T) {
	item := gjson.Parse(`{"title":"no id here"}`)
	fm := fieldMap{ID: "id", Title: "title"}
	_, _, ok := buildDocument("sa", "result", "", fm, time.RFC3339, item)
	if ok {
		t.Fatal("expected buildDocument to reject item without id")
	}
}

func TestBuildDocument_MissingUpdatedAtDefaultsZero(t *testing.T) {
	item := gjson.Parse(`{"id":"1","title":"t"}`)
	fm := fieldMap{ID: "id", Title: "title", UpdatedAt: "updated_at"}
	doc, updated, ok := buildDocument("sa", "result", "", fm, time.RFC3339, item)
	if !ok {
		t.Fatal("expected buildDocument to succeed even without updated_at")
	}
	if !updated.IsZero() || !doc.UpdatedAt.IsZero() {
		t.Errorf("updated = %v, want zero", updated)
	}
}

func TestBuildDocument_FallsBackToRFC3339WhenLayoutMismatches(t *testing.T) {
	item := gjson.Parse(`{"id":"1","updated_at":"2026-03-05T10:00:00Z"}`)
	fm := fieldMap{ID: "id", UpdatedAt: "updated_at"}
	_, updated, ok := buildDocument("sa", "result", "", fm, "2006-01-02", item)
	if !ok {
		t.Fatal("expected buildDocument to succeed")
	}
	want := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	if !updated.Equal(want) {
		t.Errorf("updated = %v, want %v (RFC3339 fallback)", updated, want)
	}
}

func TestBuildDocument_ExtraFieldMissingIsOmitted(t *testing.T) {
	item := gjson.Parse(`{"id":"1"}`)
	fm := fieldMap{ID: "id", Extra: map[string]string{"status": "meta.state"}}
	doc, _, ok := buildDocument("sa", "result", "", fm, time.RFC3339, item)
	if !ok {
		t.Fatal("expected buildDocument to succeed")
	}
	if _, exists := doc.Frontmatter["status"]; exists {
		t.Errorf("expected missing extra field to be omitted, got %+v", doc.Frontmatter)
	}
}
