package xlsx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/importer"
)

func init() {
	importer.Register(".xlsx", func() importer.FileImporter { return New() })
}

type Importer struct{}

func New() *Importer { return &Importer{} }

func (i *Importer) Ext() string { return ".xlsx" }

func (i *Importer) Import(path string) ([]connector.Document, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("xlsx: open %s: %w", path, err)
	}
	defer f.Close()

	name := filepath.Base(path)
	var updatedAt time.Time
	if info, err := os.Stat(path); err == nil {
		updatedAt = info.ModTime()
	}

	sheets := f.GetSheetList()
	docs := make([]connector.Document, 0, len(sheets))
	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		docs = append(docs, connector.Document{
			ID:        path + "#" + sheet,
			Kind:      "xlsx-sheet",
			Title:     name + " — " + sheet,
			UpdatedAt: updatedAt,
			Body:      renderSheet(sheet, rows),
			Frontmatter: map[string]any{
				"file_path": path,
				"file_name": name,
				"sheet":     sheet,
				"row_count": len(rows),
			},
		})
	}
	return docs, nil
}

func renderSheet(sheet string, rows [][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", sheet)
	if len(rows) == 0 {
		b.WriteString("_empty sheet_\n")
		return b.String()
	}

	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	pad := func(r []string) []string {
		out := make([]string, width)
		copy(out, r)
		return out
	}

	writeRow := func(r []string) {
		b.WriteString("|")
		for _, c := range pad(r) {
			b.WriteString(" " + strings.ReplaceAll(c, "|", "\\|") + " |")
		}
		b.WriteString("\n")
	}

	writeRow(rows[0])
	b.WriteString("|")
	for j := 0; j < width; j++ {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, r := range rows[1:] {
		writeRow(r)
	}
	return b.String()
}
