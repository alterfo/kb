package planner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBash_CapturesOutput(t *testing.T) {
	tr := newTools(t.TempDir())
	out, err := tr.bash(context.Background(), "printf hello")
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if out != "hello" {
		t.Errorf("out = %q", out)
	}
}

func TestBash_ReportsNonZeroExit(t *testing.T) {
	tr := newTools(t.TempDir())
	out, err := tr.bash(context.Background(), "echo boom; exit 3")
	if err != nil {
		t.Fatalf("bash should not error on non-zero exit: %v", err)
	}
	if !strings.Contains(out, "boom") || !strings.Contains(out, "[exit status 3]") {
		t.Errorf("out = %q", out)
	}
}

func TestReadFile_WithLineNumbers(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	tr := newTools(dir)
	out, err := tr.readFile(map[string]any{"path": "a.txt"})
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if !strings.Contains(out, "1\tone") || !strings.Contains(out, "3\tthree") {
		t.Errorf("out = %q", out)
	}
}

func TestReadFile_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	tr := newTools(dir)
	out, err := tr.readFile(map[string]any{"path": "a.txt", "offset": float64(2), "limit": float64(1)})
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if !strings.Contains(out, "2\ttwo") || strings.Contains(out, "three") {
		t.Errorf("out = %q", out)
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	tr := newTools(dir)
	if _, err := tr.writeFile(map[string]any{"path": "sub/b.txt", "content": "body"}); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "sub", "b.txt"))
	if err != nil || string(b) != "body" {
		t.Fatalf("file content = %q, err = %v", b, err)
	}
}

func TestEditFile_ReplacesOnce(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("hello world"), 0o644)
	tr := newTools(dir)
	_, err := tr.editFile(map[string]any{"path": "c.txt", "old_string": "world", "new_string": "go"})
	if err != nil {
		t.Fatalf("editFile: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "c.txt"))
	if string(b) != "hello go" {
		t.Errorf("content = %q", b)
	}
}

func TestEditFile_ErrorsOnMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("x x"), 0o644)
	tr := newTools(dir)
	if _, err := tr.editFile(map[string]any{"path": "c.txt", "old_string": "x", "new_string": "y"}); err == nil {
		t.Fatal("expected error for multiple matches")
	}
}

func TestResolve_RejectsEscapes(t *testing.T) {
	tr := newTools(t.TempDir())
	for _, p := range []string{"/etc/passwd", "../secret", "a/../../b"} {
		if _, err := tr.resolve(p); err == nil {
			t.Errorf("expected error for %q", p)
		}
	}
	if _, err := tr.resolve("a/b.txt"); err != nil {
		t.Errorf("unexpected error for relative path: %v", err)
	}
}

func TestGlob_FindsFiles(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.go"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "src", "b_test.go"), nil, 0o644)
	tr := newTools(dir)
	out, err := tr.glob(map[string]any{"pattern": "src/*_test.go"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if !strings.Contains(out, "src/b_test.go") || strings.Contains(out, "a.go") {
		t.Errorf("out = %q", out)
	}
}

func TestGrep_FindsMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package p\nfunc Foo() {}\n"), 0o644)
	tr := newTools(dir)
	out, err := tr.grep(map[string]any{"pattern": "func Foo"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(out, "x.go:2:") {
		t.Errorf("out = %q", out)
	}
}

func TestGit_RunsCommand(t *testing.T) {
	tr := newTools(t.TempDir())
	out, err := tr.git(context.Background(), "status --short")
	if err != nil {
		t.Fatalf("git: %v", err)
	}
	if !strings.Contains(out, "fatal") || !strings.Contains(out, "[exit status 128]") {
		t.Errorf("out = %q", out)
	}
}
