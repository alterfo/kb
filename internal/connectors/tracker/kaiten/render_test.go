package kaiten

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
	card := apiCard{
		ID:          42,
		Title:       "Fix the thing",
		Description: "Steps to reproduce.\n\nMore detail.",
		UpdatedAt:   "2026-03-04T05:06:07.000Z",
		Column:      apiColumn{Title: "In Progress"},
		Board:       apiBoard{Title: "KB"},
		Owner:       &apiUser{FullName: "Ivan Ivanov"},
	}

	d := buildDocument("kt", "https://kt.example.com", card)
	if d.Kind != "card" {
		t.Fatalf("Kind = %q, want card", d.Kind)
	}
	if d.ID != "42" {
		t.Fatalf("ID = %q, want 42", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "card.md", got)
}

func TestBuildDocument_NoOwner(t *testing.T) {
	card := apiCard{ID: 1, Title: "No owner", UpdatedAt: "2026-01-01T00:00:00.000Z"}
	d := buildDocument("kt", "https://kt.example.com", card)
	if d.Frontmatter["assignee"] != "" {
		t.Fatalf("assignee = %v, want empty", d.Frontmatter["assignee"])
	}
}
