package jsonf

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/importer"
)

func init() {
	importer.Register(".json", func() importer.FileImporter { return New() })
}

type Importer struct{}

func New() *Importer { return &Importer{} }

func (i *Importer) Ext() string { return ".json" }

func (i *Importer) Import(path string) ([]connector.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("jsonf: read %s: %w", path, err)
	}
	if !gjson.ValidBytes(data) {
		return nil, fmt.Errorf("jsonf: invalid json in %s", path)
	}

	root := gjson.ParseBytes(data)
	var lines []string
	flatten("$", root, &lines)

	name := filepath.Base(path)
	info, err := os.Stat(path)
	var updatedAt time.Time
	if err == nil {
		updatedAt = info.ModTime()
	}

	doc := connector.Document{
		ID:        path,
		Kind:      "json",
		Title:     name,
		UpdatedAt: updatedAt,
		Body:      "# " + name + "\n\n" + strings.Join(lines, "\n") + "\n",
		Frontmatter: map[string]any{
			"file_path": path,
			"file_name": name,
		},
	}
	return []connector.Document{doc}, nil
}

func flatten(path string, r gjson.Result, out *[]string) {
	switch {
	case r.IsObject():
		r.ForEach(func(key, value gjson.Result) bool {
			flatten(path+"."+key.String(), value, out)
			return true
		})
	case r.IsArray():
		r.ForEach(func(key, value gjson.Result) bool {
			flatten(path+"["+strconv.FormatInt(key.Int(), 10)+"]", value, out)
			return true
		})
	default:
		*out = append(*out, fmt.Sprintf("- `%s`: %s", path, scalarString(r)))
	}
}

func scalarString(r gjson.Result) string {
	if r.Type == gjson.Null {
		return "null"
	}
	return r.String()
}
