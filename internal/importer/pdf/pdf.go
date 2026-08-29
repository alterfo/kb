package pdf

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	ledongthucpdf "github.com/ledongthuc/pdf"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/importer"
)

func init() {
	importer.Register(".pdf", func() importer.FileImporter { return New() })
}

type Importer struct {
	PdftotextFallback bool
}

func New() *Importer { return &Importer{} }

func (i *Importer) Ext() string { return ".pdf" }

func (i *Importer) Import(path string) ([]connector.Document, error) {
	f, r, err := ledongthucpdf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("pdf: open %s: %w", path, err)
	}
	defer f.Close()

	text, err := extractText(r)
	if err != nil {
		return nil, fmt.Errorf("pdf: extract text %s: %w", path, err)
	}

	needsOCR := strings.TrimSpace(text) == ""
	if needsOCR && i.PdftotextFallback {
		if fallback, ferr := pdftotextFallback(path); ferr == nil && strings.TrimSpace(fallback) != "" {
			text = fallback
			needsOCR = false
		}
	}

	name := filepath.Base(path)
	var updatedAt time.Time
	if info, statErr := os.Stat(path); statErr == nil {
		updatedAt = info.ModTime()
	}

	doc := connector.Document{
		ID:        path,
		Kind:      "pdf",
		Title:     name,
		UpdatedAt: updatedAt,
		Body:      "# " + name + "\n\n" + strings.TrimSpace(text) + "\n",
		Frontmatter: map[string]any{
			"file_path":  path,
			"file_name":  name,
			"page_count": r.NumPage(),
			"needs_ocr":  needsOCR,
		},
	}
	return []connector.Document{doc}, nil
}

func extractText(r *ledongthucpdf.Reader) (string, error) {
	reader, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func pdftotextFallback(path string) (string, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return "", err
	}
	out, err := exec.Command("pdftotext", "-layout", path, "-").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
