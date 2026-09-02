package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var docsLinkBacktickRe = regexp.MustCompile("`([^`]+)`")

func TestDocumentationLinksResolve(t *testing.T) {
	repo := repoRoot(t)
	docs := []string{"AGENTS.md", "README.md", "CONTRIBUTING.md"}
	for _, doc := range docs {
		t.Run(doc, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(repo, doc))
			if err != nil {
				t.Fatalf("read %s: %v", doc, err)
			}
			for _, match := range docsLinkBacktickRe.FindAllStringSubmatch(string(data), -1) {
				target := strings.TrimSpace(match[1])
				if !strings.HasSuffix(target, ".md") || target == ".md" {
					continue
				}
				path := target
				if !filepath.IsAbs(path) {
					path = filepath.Join(repo, filepath.FromSlash(path))
				}
				if _, err := os.Stat(path); err != nil {
					t.Errorf("%s references missing file %s", doc, target)
				}
			}
		})
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}
