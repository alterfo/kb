package yandex

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
		Key:         "KB-42",
		Summary:     "Fix the thing",
		Description: "Steps to reproduce.\n\nMore detail.",
		Status:      apiStatus{Display: "In Progress"},
		Assignee:    &apiUser{Display: "Ivan Ivanov"},
		UpdatedAt:   "2026-03-04T05:06:07.000+0000",
	}

	d := buildDocument("yt", "https://tracker.yandex.ru", "KB", it)
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

func TestBuildDocument_NoAssignee(t *testing.T) {
	it := apiIssue{Key: "KB-1", Summary: "No assignee", Status: apiStatus{Display: "Open"}, UpdatedAt: "2026-01-01T00:00:00.000+0000"}
	d := buildDocument("yt", "https://tracker.yandex.ru", "KB", it)
	if d.Frontmatter["assignee"] != "" {
		t.Fatalf("assignee = %v, want empty", d.Frontmatter["assignee"])
	}
}
