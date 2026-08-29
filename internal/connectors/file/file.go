package file

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/importer"
	_ "github.com/alterfo/kb/internal/importer/code"
	_ "github.com/alterfo/kb/internal/importer/jsonf"
	_ "github.com/alterfo/kb/internal/importer/markdown"
	_ "github.com/alterfo/kb/internal/importer/pdf"
	_ "github.com/alterfo/kb/internal/importer/sqlddl"
	_ "github.com/alterfo/kb/internal/importer/xlsx"
)

func init() {
	registry.Register("file", func() connector.Connector { return New() })
}

type Connector struct {
	name       string
	root       string
	visibility string
}

func New() *Connector { return &Connector{} }

func (c *Connector) Type() string { return "file" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.root = strings.TrimSpace(cfg.Config["path"])
	if c.root == "" {
		return fmt.Errorf("file: source %q: config.path is required", cfg.Name)
	}
	c.visibility = cfg.Config["visibility"]
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	count := 0
	hadImportErr := false
	err := filepath.WalkDir(c.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		imp, impErr := importer.New(ext)
		if impErr != nil {
			return nil
		}
		docs, impErr := imp.Import(path)
		if impErr != nil {
			hadImportErr = true
			return nil
		}
		for _, doc := range docs {
			doc.Source = c.name
			if doc.Visibility == "" {
				doc.Visibility = c.visibility
			}
			select {
			case out <- doc:
				count++
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	if err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("file: walk %s: %w", c.root, err)
	}

	return connector.Cursor{}, connector.FetchInfo{ItemCount: count, FullReconcile: !hadImportErr}, nil
}
