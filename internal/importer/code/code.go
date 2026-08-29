package code

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/importer"
)

func init() {
	importer.Register(".go", func() importer.FileImporter { return New() })
}

type Importer struct{}

func New() *Importer { return &Importer{} }

func (i *Importer) Ext() string { return ".go" }

func (i *Importer) Import(path string) ([]connector.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("code: read %s: %w", path, err)
	}
	var updatedAt time.Time
	if info, statErr := os.Stat(path); statErr == nil {
		updatedAt = info.ModTime()
	}
	name := filepath.Base(path)
	doc := connector.Document{
		ID:        path,
		Kind:      "code",
		Title:     name,
		UpdatedAt: updatedAt,
		Body:      string(data),
		Frontmatter: map[string]any{
			"file_path": path,
			"file_name": name,
		},
	}
	return []connector.Document{doc}, nil
}
