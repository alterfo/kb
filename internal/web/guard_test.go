package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWithin_OK(t *testing.T) {
	root := t.TempDir()
	abs, err := resolveWithin(root, "notes/sub/a.md")
	if err != nil {
		t.Fatalf("resolveWithin: %v", err)
	}
	if !strings.HasPrefix(abs, root) {
		t.Errorf("abs = %q, want under root %q", abs, root)
	}
}

func TestResolveWithin_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"../escape.md", "../../etc/passwd", "notes/../../x.md", "/etc/passwd"} {
		if _, err := resolveWithin(root, p); err == nil {
			t.Errorf("resolveWithin(%q): expected error", p)
		}
	}
}

func TestResolveWithin_RejectsAbsolutePathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	target := filepath.Join(other, "secret.md")
	if _, err := resolveWithin(root, target); err == nil {
		t.Errorf("resolveWithin(%q): expected error for path outside root", target)
	}
}

func TestResolveWithin_RejectsSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	link := filepath.Join(root, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := resolveWithin(root, "link.md"); err == nil {
		t.Fatal("resolveWithin(link.md): expected error for symlink escaping root")
	}
}

func TestResolveWithin_RejectsNewFileUnderSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "notes")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := resolveWithin(root, "notes/new.md"); err == nil {
		t.Fatal("resolveWithin(notes/new.md): expected error for new file under symlinked parent")
	}
}
