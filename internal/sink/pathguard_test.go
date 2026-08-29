package sink

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWithin_NonexistentRootUnderSymlinkedAncestor(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	root := filepath.Join(link, "kb-root")

	got, err := ResolveWithin(root, "note.md")
	if err != nil {
		t.Fatalf("ResolveWithin: %v", err)
	}
	want := filepath.Join(root, "note.md")
	if got != want {
		t.Fatalf("ResolveWithin = %q, want %q", got, want)
	}
}
