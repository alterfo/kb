package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAddNote_WritesIndexesAndIsImmediatelySearchable(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()

	_, out, err := te.server.addNote(ctx, nil, addNoteIn{Path: "notes/my-note.md", Title: "My Note", Content: "unique-zebra-token appears here"})
	if err != nil {
		t.Fatalf("addNote: %v", err)
	}
	if out.Path != "notes/my-note.md" || out.ID != "my-note" {
		t.Fatalf("addNote: out = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(te.root, "notes", "my-note.md")); err != nil {
		t.Fatalf("addNote: file not written: %v", err)
	}

	_, sout, err := te.server.search(ctx, nil, searchIn{Query: "unique-zebra-token"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(sout.Results) == 0 {
		t.Fatalf("search: note not found immediately after add_note (BM25 not invalidated)")
	}
}

func TestAddNote_BareFilenameDefaultsToNotesSource(t *testing.T) {
	te := newTestEnv(t, nil)
	_, out, err := te.server.addNote(context.Background(), nil, addNoteIn{Path: "my-note", Content: "hi"})
	if err != nil {
		t.Fatalf("addNote: %v", err)
	}
	if out.Path != "notes/my-note.md" {
		t.Fatalf("addNote: Path = %q, want notes/my-note.md", out.Path)
	}
}

func TestAddNote_RequiresContent(t *testing.T) {
	te := newTestEnv(t, nil)
	if _, _, err := te.server.addNote(context.Background(), nil, addNoteIn{Path: "notes/empty.md"}); err == nil {
		t.Fatalf("addNote: got nil error for empty content, want error")
	}
}

func TestAddNote_PathTraversalRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	if _, _, err := te.server.addNote(context.Background(), nil, addNoteIn{Path: "../escape.md", Content: "x"}); err == nil {
		t.Fatalf("addNote: path traversal was not rejected")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(te.root), "escape.md")); err == nil {
		t.Fatalf("addNote: traversal file was written outside root")
	}
}
