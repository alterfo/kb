package report

import (
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/store/vector"
)

func TestSupersessionBlockEmpty(t *testing.T) {
	chunks := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "a", FileName: "a.md"}},
	}
	if got := SupersessionBlock(chunks); got != "" {
		t.Fatalf("SupersessionBlock = %q, want empty without superseded chunks", got)
	}
}

func TestSupersessionBlockFormatsPairs(t *testing.T) {
	chunks := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "old", RefDocID: "doc-old", FileName: "old.md", FilePath: "notes/old.md", SupersededBy: "doc-new", Metadata: map[string]string{"last_updated": "2026-01-05"}}, Score: 1},
		{Chunk: vector.Chunk{ID: "new", RefDocID: "doc-new", FileName: "new.md", FilePath: "notes/new.md", Metadata: map[string]string{"last_updated": "2026-02-09"}}, Score: 2},
	}
	got := SupersessionBlock(chunks)
	if got == "" {
		t.Fatal("expected non-empty block for superseded pair")
	}
	if !strings.Contains(got, "Superseded documents detected") {
		t.Errorf("block missing header: %q", got)
	}
	if !strings.Contains(got, "old.md") || !strings.Contains(got, "new.md") {
		t.Errorf("block must name both versions: %q", got)
	}
	if !strings.Contains(got, "2026-01-05") || !strings.Contains(got, "2026-02-09") {
		t.Errorf("block should carry document dates when known: %q", got)
	}
}

func TestBuildSynthesisPromptIncludesBlock(t *testing.T) {
	chunks := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "old", RefDocID: "doc-old", FileName: "old.md", Text: "old text", SupersededBy: "doc-new"}, Score: 1},
		{Chunk: vector.Chunk{ID: "new", RefDocID: "doc-new", FileName: "new.md", Text: "new text"}, Score: 2},
	}
	prompt := buildSynthesisPrompt("pricing?", chunks)
	if !strings.Contains(prompt, "Superseded documents detected") {
		t.Fatalf("prompt missing supersession block:\n%s", prompt)
	}
}

func TestSynthesisPromptKeepsMarkerPrefix(t *testing.T) {
	if !strings.HasPrefix(searchSynthesisSystemPrompt, "You answer a user's search query using ONLY the provided excerpts") {
		t.Fatal("search synthesis marker prefix changed")
	}
	if !strings.Contains(searchSynthesisSystemPrompt, "exact numbers") {
		t.Error("search synthesis prompt missing strict answer protocol")
	}
}
