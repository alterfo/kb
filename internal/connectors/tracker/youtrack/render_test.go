package youtrack

import (
	"os"
	"path/filepath"
	"testing"

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
	it := apiIssue{
		IDReadable:  "KB-42",
		Summary:     "Fix the thing",
		Description: "Steps to reproduce.\n\nMore detail.",
		Updated:     1772859967000,
		Project:     apiProject{ShortName: "KB"},
		CustomField: []apiCustomField{
			{Name: "State", Value: &apiFieldValue{Name: "In Progress"}},
			{Name: "Assignee", Value: &apiFieldValue{Login: "ivanov"}},
		},
	}

	d := buildDocument("yt", "https://yt.example.com", it)
	if d.Kind != "issue" {
		t.Fatalf("Kind = %q, want issue", d.Kind)
	}
	if d.ID != "KB-42" {
		t.Fatalf("ID = %q, want KB-42", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "issue.md", got)
}

func TestCustomField_MissingReturnsEmpty(t *testing.T) {
	it := apiIssue{IDReadable: "KB-1"}
	if got := it.status(); got != "" {
		t.Fatalf("status() = %q, want empty", got)
	}
	if got := it.assignee(); got != "" {
		t.Fatalf("assignee() = %q, want empty", got)
	}
}
