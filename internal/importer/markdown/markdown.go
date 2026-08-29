package markdown

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/importer"
	"github.com/alterfo/kb/internal/importer/legalru"
)

func init() {
	importer.Register(".md", func() importer.FileImporter { return New() })
}

type Importer struct{}

func New() *Importer { return &Importer{} }

func (i *Importer) Ext() string { return ".md" }

func (i *Importer) Import(path string) ([]connector.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("markdown: read %s: %w", path, err)
	}

	docs, err := legalru.New().Import(path)
	if err != nil {
		return nil, err
	}
	if len(docs) > 0 {
		return docs, nil
	}

	fm, body, title := parse(string(data), filepath.Base(path))

	var updatedAt time.Time
	if info, err := os.Stat(path); err == nil {
		updatedAt = info.ModTime()
	}

	doc := connector.Document{
		ID:        path,
		Kind:      "md",
		Title:     title,
		UpdatedAt: updatedAt,
		Body:      body,
		Frontmatter: map[string]any{
			"file_path": path,
			"file_name": filepath.Base(path),
		},
	}
	for k, v := range fm {
		doc.Frontmatter[k] = v
	}
	return []connector.Document{doc}, nil
}

func parse(src, fallback string) (map[string]any, string, string) {
	fm := map[string]any{}
	body := src
	if meta, rest, ok := splitFrontmatter(src); ok {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(meta), &m); err == nil {
			for k, v := range m {
				fm[k] = v
			}
		}
		body = rest
	}
	title := fallback
	if t, ok := fm["title"].(string); ok && strings.TrimSpace(t) != "" {
		title = strings.TrimSpace(t)
	} else if h := firstHeading(body); h != "" {
		title = h
	}
	return fm, body, title
}

func splitFrontmatter(src string) (meta, rest string, ok bool) {
	lines := strings.Split(src, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", src, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", src, false
}

func firstHeading(src string) string {
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(t[2:])
		}
	}
	return ""
}
