package sink

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/render"
)

type FileSink struct {
	root string
}

func NewFileSink(root string) *FileSink {
	return &FileSink{root: root}
}

func (s *FileSink) Write(ctx context.Context, d connector.Document) error {
	data, err := render.Render(d)
	if err != nil {
		return fmt.Errorf("filesink: render %s/%s: %w", d.Source, d.ID, err)
	}
	path := s.path(d.Source, d.ID)
	if err := WriteFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("filesink: write %s: %w", path, err)
	}
	return nil
}

func (s *FileSink) Prune(ctx context.Context, sourceName string, seen map[string]struct{}, prefixes ...string) error {
	dir := filepath.Join(s.root, sanitizeID(sourceName))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("filesink: prune readdir %s: %w", dir, err)
	}

	seenFiles := make(map[string]struct{}, len(seen))
	for id := range seen {
		seenFiles[sanitizeID(id)+".md"] = struct{}{}
	}

	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if _, ok := seenFiles[name]; ok {
			continue
		}
		if len(prefixes) > 0 {
			id, ok := frontmatterID(filepath.Join(dir, name))
			// Unknown/unparsable files are preserved when pruning is
			// scoped: they may belong to an incremental-only category.
			if !ok || !idInPrefixes(id, prefixes) {
				continue
			}
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("filesink: prune remove %s: %w", name, err)
		}
	}
	return firstErr
}

// frontmatterID returns the document id recorded in the file's frontmatter.
func frontmatterID(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	d, err := render.Parse(data)
	if err != nil {
		return "", false
	}
	return d.ID, d.ID != ""
}

func idInPrefixes(id string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

func (s *FileSink) Tombstone(ctx context.Context, sourceName, id string) error {
	err := os.Remove(s.path(sourceName, id))
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("filesink: tombstone %s/%s: %w", sourceName, id, err)
}

func (s *FileSink) path(sourceName, id string) string {
	return filepath.Join(s.root, sanitizeID(sourceName), sanitizeID(id)+".md")
}

// WritePath writes data to root/<relPath> atomically, creating parent
// directories as needed. relPath must already be validated to stay within
// root; callers run a resolveWithin-style check first. Unlike FileSink
// writes it honors subdirectories exactly, so notes can land at e.g.
// notes/approved/foo.md instead of being flattened.
func WritePath(root, relPath string, data []byte) error {
	if filepath.IsAbs(relPath) {
		return fmt.Errorf("sink: write %s: absolute path not allowed", relPath)
	}
	abs, err := ResolveWithin(root, filepath.FromSlash(relPath))
	if err != nil {
		return fmt.Errorf("sink: write %s: %w", relPath, err)
	}
	if err := WriteFileAtomic(abs, data, 0o644); err != nil {
		return fmt.Errorf("sink: write %s: %w", relPath, err)
	}
	return nil
}

func sanitizeID(id string) string {
	var b strings.Builder
	replaced := false
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
			replaced = true
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	// A source named "." or ".." must not escape the sink root through
	// filepath.Join(root, sanitizeID(sourceName)).
	if b.String() == "." || b.String() == ".." {
		return "_"
	}
	// Distinct ids whose sanitized forms collapse together (e.g. "a#b" and
	// "a_b") would overwrite each other on disk; append a short hash of the
	// raw id when anything was replaced so clean ids stay byte-identical
	// and colliding ones stay distinct.
	if replaced {
		sum := sha256.Sum256([]byte(id))
		fmt.Fprintf(&b, "-%x", sum[:4])
	}
	return b.String()
}
