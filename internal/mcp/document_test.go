package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

func TestGetDocument_ReturnsBodyAndFrontmatter(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/doc1.md", connector.Document{
		ID: "doc1", Source: "notes", Title: "Doc One", Body: "hello world",
		Frontmatter: map[string]any{"project": "kb"},
	})

	_, out, err := te.server.getDocument(context.Background(), nil, getDocumentIn{Path: "notes/doc1.md"})
	if err != nil {
		t.Fatalf("getDocument: %v", err)
	}
	if out.Body != "hello world" || out.Title != "Doc One" || out.Source != "notes" {
		t.Fatalf("getDocument: out = %+v", out)
	}
	if out.Frontmatter["project"] != "kb" {
		t.Fatalf("getDocument: frontmatter = %+v, want project=kb", out.Frontmatter)
	}
}

func TestGetDocument_PlainMarkdownWithoutFrontmatterFailsOpen(t *testing.T) {
	te := newTestEnv(t, nil)
	full := filepath.Join(te.root, "plain.md")
	if err := os.WriteFile(full, []byte("just text, no frontmatter"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, out, err := te.server.getDocument(context.Background(), nil, getDocumentIn{Path: "plain.md"})
	if err != nil {
		t.Fatalf("getDocument: %v", err)
	}
	if out.Body != "just text, no frontmatter" {
		t.Fatalf("getDocument: body = %q", out.Body)
	}
}

func TestGetDocument_MissingFileReturnsError(t *testing.T) {
	te := newTestEnv(t, nil)
	if _, _, err := te.server.getDocument(context.Background(), nil, getDocumentIn{Path: "nope.md"}); err == nil {
		t.Fatalf("getDocument: got nil error for missing file, want error")
	}
}

func TestGetDocument_PathTraversalRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	// A secret file just outside root, e.g. sibling to KB_ROOT.
	outside := filepath.Join(filepath.Dir(te.root), "secret.md")
	if err := os.WriteFile(outside, []byte("top secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	if _, _, err := te.server.getDocument(context.Background(), nil, getDocumentIn{Path: "../secret.md"}); err == nil {
		t.Fatalf("getDocument: path traversal was not rejected")
	}
}
