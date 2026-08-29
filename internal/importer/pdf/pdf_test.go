package pdf

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExt(t *testing.T) {
	if got := New().Ext(); got != ".pdf" {
		t.Fatalf("Ext() = %q, want .pdf", got)
	}
}

func TestImport_TextExtracted(t *testing.T) {
	docs, err := New().Import("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	d := docs[0]
	if d.Kind != "pdf" {
		t.Errorf("Kind = %q, want pdf", d.Kind)
	}
	if !strings.Contains(d.Body, "Hello GraphRAG knowledge base") {
		t.Errorf("Body missing extracted text:\n%s", d.Body)
	}
	if d.Frontmatter["needs_ocr"] != false {
		t.Errorf("needs_ocr = %v, want false", d.Frontmatter["needs_ocr"])
	}
	if d.Frontmatter["page_count"] != 1 {
		t.Errorf("page_count = %v, want 1", d.Frontmatter["page_count"])
	}
}

func TestImport_NoTextMarksNeedsOCR(t *testing.T) {
	docs, err := New().Import("testdata/no_text.pdf")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Frontmatter["needs_ocr"] != true {
		t.Errorf("needs_ocr = %v, want true", docs[0].Frontmatter["needs_ocr"])
	}
}

func TestImport_MissingFile(t *testing.T) {
	if _, err := New().Import("testdata/does-not-exist.pdf"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestImport_PdftotextFallbackDisabledByDefault(t *testing.T) {
	imp := New()
	if imp.PdftotextFallback {
		t.Fatal("expected PdftotextFallback to default to false")
	}
}

func TestImport_PdftotextFallbackRecoversText(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not available on PATH")
	}
	imp := New()
	imp.PdftotextFallback = true
	docs, err := imp.Import("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !strings.Contains(docs[0].Body, "Hello GraphRAG") {
		t.Errorf("Body missing text via pdftotext fallback:\n%s", docs[0].Body)
	}
}

func TestPdftotextFallbackBinaryMissing(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := pdftotextFallback("testdata/sample.pdf"); err == nil {
		t.Fatal("expected error when pdftotext is not on PATH")
	}
}
