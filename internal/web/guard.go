package web

import (
	"fmt"

	"github.com/alterfo/kb/internal/sink"
)

func resolveWithin(root, relPath string) (string, error) {
	abs, err := sink.ResolveWithin(root, relPath)
	if err != nil {
		return "", fmt.Errorf("web: %w", err)
	}
	return abs, nil
}
