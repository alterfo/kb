package governance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TrashDirName is the root-relative directory soft-deleted documents are
// moved into. Indexer.BuildAll/Scan skip dot-directories, so it never gets
// re-indexed or rescanned.
const TrashDirName = ".trash"

var ErrNotFound = errors.New("governance: document not found")

// Trash implements soft-delete for KB documents: SoftDelete moves a file
// into root/.trash/<rel path> (never hard-deleted until Empty) so deletions
// are recoverable via Restore.
type Trash struct {
	root string
}

func NewTrash(root string) *Trash {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &Trash{root: root}
}

func (t *Trash) dir() string {
	return filepath.Join(t.root, TrashDirName)
}

// SoftDelete moves the document at root-relative relPath into the trash.
// Returns the resulting path relative to root (inside .trash/).
func (t *Trash) SoftDelete(relPath string) (string, error) {
	src, err := resolveWithin(t.root, relPath)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(src); statErr != nil || info.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNotFound, relPath)
	}

	dest := uniqueDestination(filepath.Join(t.dir(), relPath))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("governance: trash %s: %w", relPath, err)
	}
	if err := os.Rename(src, dest); err != nil {
		return "", fmt.Errorf("governance: trash %s: %w", relPath, err)
	}
	return t.relTo(dest)
}

// Restore moves a trashed document (trashRelPath, relative to root — i.e.
// starting with ".trash/", as returned by SoftDelete/List) back to its
// original location. Returns the restored path relative to root.
func (t *Trash) Restore(trashRelPath string) (string, error) {
	src, err := resolveWithin(t.root, trashRelPath)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(src); statErr != nil || info.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNotFound, trashRelPath)
	}
	trashAbs := t.dir()
	if src != trashAbs && !strings.HasPrefix(src, trashAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("governance: restore %s: path is not inside .trash", trashRelPath)
	}

	orig, err := filepath.Rel(t.dir(), src)
	if err != nil {
		return "", fmt.Errorf("governance: restore %s: %w", trashRelPath, err)
	}
	dest := uniqueDestination(filepath.Join(t.root, orig))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("governance: restore %s: %w", trashRelPath, err)
	}
	if err := os.Rename(src, dest); err != nil {
		return "", fmt.Errorf("governance: restore %s: %w", trashRelPath, err)
	}
	return t.relTo(dest)
}

// TrashEntry describes one file sitting in the trash.
type TrashEntry struct {
	Path    string // relative to .trash/
	Size    int64
	ModTime time.Time
}

// List returns every file currently in the trash, sorted by path.
func (t *Trash) List() ([]TrashEntry, error) {
	dir := t.dir()
	var out []TrashEntry
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == dir {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		out = append(out, TrashEntry{Path: filepath.ToSlash(rel), Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("governance: list trash: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Empty permanently deletes everything in the trash. Returns the number of
// files removed.
func (t *Trash) Empty() (int, error) {
	entries, err := t.List()
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	if err := os.RemoveAll(t.dir()); err != nil {
		return 0, fmt.Errorf("governance: empty trash: %w", err)
	}
	return len(entries), nil
}

func (t *Trash) relTo(dest string) (string, error) {
	rootAbs, err := filepath.Abs(t.root)
	if err != nil {
		return "", fmt.Errorf("governance: resolve root: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, dest)
	if err != nil {
		return "", fmt.Errorf("governance: relativize %s: %w", dest, err)
	}
	return filepath.ToSlash(rel), nil
}

func resolveWithin(root, relPath string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("governance: resolve root: %w", err)
	}
	full := filepath.Join(rootAbs, relPath)
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("governance: resolve path: %w", err)
	}
	if fullAbs != rootAbs && !strings.HasPrefix(fullAbs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("governance: path escapes root: %s", relPath)
	}
	return fullAbs, nil
}

func uniqueDestination(dest string) string {
	if _, err := os.Stat(dest); err != nil {
		return dest
	}
	ext := filepath.Ext(dest)
	stem := strings.TrimSuffix(dest, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
}
