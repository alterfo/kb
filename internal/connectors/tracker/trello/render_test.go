package trello

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
	card := trelloCard{
		ID:       "card-42",
		Name:     "Add voice commands",
		Desc:     "Steps to reproduce.\n\nMore detail.",
		Due:      "2026-08-22T00:00:00.000Z",
		ShortURL: "https://trello.com/c/card42",
		IDList:   "list-1",
		Labels: []trelloLabel{
			{ID: "label-1", Name: "Feature"},
			{ID: "label-2", Name: "Core"},
		},
	}

	d := buildDocument("leon-trello", "board-7", "Roadmap", card)
	if d.Kind != "trello_card" {
		t.Fatalf("Kind = %q, want trello_card", d.Kind)
	}
	if d.ID != "board-7-card-42" {
		t.Fatalf("ID = %q", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "trello_card.md", got)
}

func TestBuildDocument_EmptyLabels(t *testing.T) {
	card := trelloCard{ID: "c", Name: "No labels"}
	d := buildDocument("src", "b", "List", card)
	if got := d.Frontmatter["labels"]; got != "" {
		t.Fatalf("labels = %v, want empty", got)
	}
}
