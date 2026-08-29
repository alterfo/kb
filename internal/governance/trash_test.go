package governance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSoftDeleteAndRestore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "hello")
	tr := NewTrash(root)

	trashed, err := tr.SoftDelete("notes/a.md")
	if err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if trashed != ".trash/notes/a.md" {
		t.Fatalf("trashed = %q, want .trash/notes/a.md", trashed)
	}
	mustNotExist(t, root, "notes/a.md")
	mustExist(t, root, trashed)

	restored, err := tr.Restore(trashed)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored != "notes/a.md" {
		t.Fatalf("restored = %q, want notes/a.md", restored)
	}
	mustExist(t, root, "notes/a.md")
	mustNotExist(t, root, trashed)
}

func TestSoftDeleteMissingFile(t *testing.T) {
	root := t.TempDir()
	tr := NewTrash(root)
	if _, err := tr.SoftDelete("notes/missing.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSoftDeletePathEscapesRoot(t *testing.T) {
	root := t.TempDir()
	tr := NewTrash(root)
	if _, err := tr.SoftDelete("../outside.md"); err == nil {
		t.Fatal("expected error for path escaping root")
	}
}

func TestSoftDeleteUniqueDestination(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "first")
	tr := NewTrash(root)

	if _, err := tr.SoftDelete("notes/a.md"); err != nil {
		t.Fatalf("SoftDelete 1: %v", err)
	}
	writeFile(t, root, "notes/a.md", "second")
	trashed2, err := tr.SoftDelete("notes/a.md")
	if err != nil {
		t.Fatalf("SoftDelete 2: %v", err)
	}
	if trashed2 != ".trash/notes/a-2.md" {
		t.Fatalf("trashed2 = %q, want .trash/notes/a-2.md", trashed2)
	}
}

func TestRestoreMissingFile(t *testing.T) {
	root := t.TempDir()
	tr := NewTrash(root)
	if _, err := tr.Restore(".trash/notes/missing.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRestoreRejectsNonTrashPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "live file")
	tr := NewTrash(root)

	if _, err := tr.Restore("notes/a.md"); err == nil {
		t.Fatal("expected error when restoring a path outside .trash")
	}
	mustExist(t, root, "notes/a.md")
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "notes", "a.md")); !os.IsNotExist(err) {
		t.Fatalf("live file moved outside root: %v", err)
	}
}

func TestListAndEmpty(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "a")
	writeFile(t, root, "notes/b.md", "b")
	tr := NewTrash(root)

	if entries, err := tr.List(); err != nil || len(entries) != 0 {
		t.Fatalf("List on empty trash = %+v, %v", entries, err)
	}

	if _, err := tr.SoftDelete("notes/a.md"); err != nil {
		t.Fatalf("SoftDelete a: %v", err)
	}
	if _, err := tr.SoftDelete("notes/b.md"); err != nil {
		t.Fatalf("SoftDelete b: %v", err)
	}

	entries, err := tr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List = %+v, want 2 entries", entries)
	}
	if entries[0].Path != "notes/a.md" || entries[1].Path != "notes/b.md" {
		t.Fatalf("List paths = %+v", entries)
	}

	n, err := tr.Empty()
	if err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if n != 2 {
		t.Fatalf("Empty removed %d, want 2", n)
	}
	entries, err = tr.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("List after Empty = %+v, %v", entries, err)
	}
}

func TestSoftDeleteAndRestoreRelativeRoot(t *testing.T) {
	root, err := os.MkdirTemp(".", "kb-trash-rel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	writeFile(t, root, "notes/a.md", "hello")
	tr := NewTrash(root)

	trashed, err := tr.SoftDelete("notes/a.md")
	if err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if trashed != ".trash/notes/a.md" {
		t.Fatalf("trashed = %q, want .trash/notes/a.md", trashed)
	}
	mustNotExist(t, root, "notes/a.md")
	mustExist(t, root, trashed)

	restored, err := tr.Restore(trashed)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored != "notes/a.md" {
		t.Fatalf("restored = %q, want notes/a.md", restored)
	}
	mustExist(t, root, "notes/a.md")
	mustNotExist(t, root, trashed)
}
