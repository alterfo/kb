package sink

import (
	"fmt"
	"path/filepath"
	"strings"
)

func ResolveWithin(root, relPath string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("sink: resolve root: %w", err)
	}
	rootAbs = filepath.Clean(rootAbs)

	fullAbs := relPath
	if !filepath.IsAbs(relPath) {
		fullAbs = filepath.Join(rootAbs, relPath)
	}
	if a, aerr := filepath.Abs(fullAbs); aerr == nil {
		fullAbs = filepath.Clean(a)
	}
	if fullAbs != rootAbs && !strings.HasPrefix(fullAbs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("sink: path %q escapes root", relPath)
	}

	rootResolved := resolveNearestExisting(rootAbs)
	fullResolved := resolveNearestExisting(fullAbs)
	if fullResolved != rootResolved && !strings.HasPrefix(fullResolved, rootResolved+string(filepath.Separator)) {
		return "", fmt.Errorf("sink: path %q resolves outside root", relPath)
	}
	return fullAbs, nil
}

func resolveNearestExisting(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	ancestor := filepath.Dir(path)
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			rel, relErr := filepath.Rel(ancestor, path)
			if relErr != nil {
				return filepath.Clean(path)
			}
			return filepath.Clean(filepath.Join(resolved, rel))
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return filepath.Clean(path)
		}
		ancestor = parent
	}
}
