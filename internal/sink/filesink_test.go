package sink

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/render"
)

func TestFileSinkWriteCreatesFile(t *testing.T) {
	root := t.TempDir()
	s := NewFileSink(root)
	d := connector.Document{ID: "42", Source: "github-myorg", Title: "Hello", Body: "world"}

	if err := s.Write(context.Background(), d); err != nil {
		t.Fatalf("Write: %v", err)
	}

	path := filepath.Join(root, "github-myorg", "42.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	want, err := render.Render(d)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("file content mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestFileSinkWriteSanitizesUnsafeID(t *testing.T) {
	root := t.TempDir()
	s := NewFileSink(root)
	d := connector.Document{ID: "a/b:c", Source: "wiki docs", Title: "T", Body: "b"}

	if err := s.Write(context.Background(), d); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rootEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(root): %v", err)
	}
	if len(rootEntries) != 1 || !rootEntries[0].IsDir() {
		t.Fatalf("expected exactly one source dir, got %v", rootEntries)
	}
	dir := filepath.Join(root, rootEntries[0].Name())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	if len(entries) != 1 || entries[0].Name() != "a_b_c-3b07f80c.md" {
		t.Fatalf("expected exactly a_b_c-3b07f80c.md, got %v", entries)
	}
	path := filepath.Join(dir, entries[0].Name())
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected sanitized path %s to exist: %v", path, err)
	}
}

func TestFileSinkSanitizeIDCollisionsStayDistinct(t *testing.T) {
	root := t.TempDir()
	s := NewFileSink(root)
	ctx := context.Background()

	if err := s.Write(ctx, connector.Document{ID: "a#b", Source: "gh", Title: "T", Body: "one"}); err != nil {
		t.Fatalf("Write(a#b): %v", err)
	}
	if err := s.Write(ctx, connector.Document{ID: "a_b", Source: "gh", Title: "T", Body: "two"}); err != nil {
		t.Fatalf("Write(a_b): %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(root, "gh"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 distinct files, got %d: %v", len(entries), entries)
	}
}

func TestFileSinkSourceNameCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	s := NewFileSink(root)

	// A source named ".." (or ".") must be contained inside the sink root,
	// not resolved to the parent directory.
	d := connector.Document{ID: "doc1", Source: "..", Title: "T", Body: "body"}
	if err := s.Write(context.Background(), d); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "_", "doc1.md")); err != nil {
		t.Fatalf("document not written to the contained path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "doc1.md")); !os.IsNotExist(err) {
		t.Fatalf("document escaped the sink root: %v", err)
	}

	// Pruning the same source must only touch files inside root.
	parent := filepath.Join(filepath.Dir(root), "keep.md")
	if err := os.WriteFile(parent, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write parent file: %v", err)
	}
	if err := s.Prune(context.Background(), "..", map[string]struct{}{}); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(parent); err != nil {
		t.Fatalf("prune escaped the sink root and deleted %s: %v", parent, err)
	}
}

func TestWritePathHonorsSubdirectories(t *testing.T) {
	root := t.TempDir()
	if err := WritePath(root, "notes/approved/foo.md", []byte("hello")); err != nil {
		t.Fatalf("WritePath: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes", "approved", "foo.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want hello", data)
	}
	if err := WritePath(root, "../escape.md", []byte("x")); err == nil {
		t.Fatalf("WritePath: expected escape rejection")
	}
}

func TestWritePathRejectsSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "notes")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := WritePath(root, "notes/new.md", []byte("x")); err == nil {
		t.Fatal("WritePath: expected rejection for new file under symlinked parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.md")); !os.IsNotExist(err) {
		t.Fatalf("WritePath wrote through symlinked parent: %v", err)
	}
}

func TestFileSinkPruneRemovesUnseen(t *testing.T) {
	root := t.TempDir()
	s := NewFileSink(root)
	ctx := context.Background()

	for _, id := range []string{"1", "2", "3"} {
		if err := s.Write(ctx, connector.Document{ID: id, Source: "github-myorg", Title: "T", Body: "b"}); err != nil {
			t.Fatalf("Write(%s): %v", id, err)
		}
	}

	seen := map[string]struct{}{"1": {}, "3": {}}
	if err := s.Prune(ctx, "github-myorg", seen); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	dir := filepath.Join(root, "github-myorg")
	for _, id := range []string{"1", "3"} {
		if _, err := os.Stat(filepath.Join(dir, id+".md")); err != nil {
			t.Fatalf("expected %s.md to survive prune: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "2.md")); !os.IsNotExist(err) {
		t.Fatalf("expected 2.md to be pruned, stat err = %v", err)
	}
}

func TestFileSinkPruneScopedToPrefixes(t *testing.T) {
	root := t.TempDir()
	s := NewFileSink(root)
	ctx := context.Background()

	ids := []string{"acme/widgets:contents:README.md", "acme/widgets:wiki:Home", "acme/widgets:issue:12"}
	for _, id := range ids {
		if err := s.Write(ctx, connector.Document{ID: id, Source: "gh", Title: "T", Body: "b"}); err != nil {
			t.Fatalf("Write(%s): %v", id, err)
		}
	}

	// Empty seen with a contents/wiki scope: only those categories are
	// eligible for removal; the incremental issue file must survive.
	if err := s.Prune(ctx, "gh", map[string]struct{}{}, "acme/widgets:contents:", "acme/widgets:wiki:"); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	dir := filepath.Join(root, "gh")
	for _, id := range []string{"acme/widgets:contents:README.md", "acme/widgets:wiki:Home"} {
		if _, err := os.Stat(filepath.Join(dir, sanitizeID(id)+".md")); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be pruned, stat err = %v", id, err)
		}
	}
	keep := filepath.Join(dir, sanitizeID("acme/widgets:issue:12")+".md")
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("expected %s to survive scoped prune: %v", "acme/widgets:issue:12", err)
	}
}

func TestFileSinkPruneNonexistentDirNoError(t *testing.T) {
	root := t.TempDir()
	s := NewFileSink(root)
	if err := s.Prune(context.Background(), "never-written", map[string]struct{}{}); err != nil {
		t.Fatalf("Prune on missing dir: %v", err)
	}
}

func TestFileSinkTombstoneRemovesFile(t *testing.T) {
	root := t.TempDir()
	s := NewFileSink(root)
	ctx := context.Background()
	d := connector.Document{ID: "42", Source: "github-myorg", Title: "T", Body: "b"}

	if err := s.Write(ctx, d); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Tombstone(ctx, "github-myorg", "42"); err != nil {
		t.Fatalf("Tombstone: %v", err)
	}

	path := filepath.Join(root, "github-myorg", "42.md")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err = %v", err)
	}
}

func TestFileSinkTombstoneNonexistentNoError(t *testing.T) {
	root := t.TempDir()
	s := NewFileSink(root)
	if err := s.Tombstone(context.Background(), "github-myorg", "missing"); err != nil {
		t.Fatalf("Tombstone on missing file: %v", err)
	}
}
