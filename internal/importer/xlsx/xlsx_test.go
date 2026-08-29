package xlsx

import (
	"strings"
	"testing"
)

func TestExt(t *testing.T) {
	if got := New().Ext(); got != ".xlsx" {
		t.Fatalf("Ext() = %q, want .xlsx", got)
	}
}

func TestImport_MultiSheet(t *testing.T) {
	docs, err := New().Import("testdata/sample.xlsx")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 sheet documents, got %d", len(docs))
	}

	bySheet := map[string]int{}
	for i, d := range docs {
		sheet, _ := d.Frontmatter["sheet"].(string)
		bySheet[sheet] = i
		if d.Kind != "xlsx-sheet" {
			t.Errorf("Kind = %q, want xlsx-sheet", d.Kind)
		}
	}
	if _, ok := bySheet["Employees"]; !ok {
		t.Fatal("expected Employees sheet")
	}
	if _, ok := bySheet["Budget"]; !ok {
		t.Fatal("expected Budget sheet")
	}

	emp := docs[bySheet["Employees"]]
	if !strings.Contains(emp.Body, "| Name | Role | Team |") {
		t.Errorf("Employees body missing header row:\n%s", emp.Body)
	}
	if !strings.Contains(emp.Body, "Alice") || !strings.Contains(emp.Body, "Platform") {
		t.Errorf("Employees body missing data row:\n%s", emp.Body)
	}
	if emp.Frontmatter["row_count"] != 3 {
		t.Errorf("Employees row_count = %v, want 3", emp.Frontmatter["row_count"])
	}

	budget := docs[bySheet["Budget"]]
	if !strings.Contains(budget.Body, "Quarter") || !strings.Contains(budget.Body, "1500") {
		t.Errorf("Budget body missing content:\n%s", budget.Body)
	}
}

func TestImport_MissingFile(t *testing.T) {
	if _, err := New().Import("testdata/does-not-exist.xlsx"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRenderSheet_EmptySheet(t *testing.T) {
	body := renderSheet("Empty", nil)
	if !strings.Contains(body, "_empty sheet_") {
		t.Errorf("expected empty-sheet marker, got:\n%s", body)
	}
}

func TestRenderSheet_RaggedRows(t *testing.T) {
	body := renderSheet("Ragged", [][]string{
		{"a", "b", "c"},
		{"1"},
	})
	if !strings.Contains(body, "| a | b | c |") {
		t.Errorf("missing header:\n%s", body)
	}
	if !strings.Contains(body, "| 1 |  |  |") {
		t.Errorf("expected padded short row:\n%s", body)
	}
}

func TestRenderSheet_EscapesPipes(t *testing.T) {
	body := renderSheet("Pipes", [][]string{
		{"h"},
		{"a|b"},
	})
	if !strings.Contains(body, `a\|b`) {
		t.Errorf("expected escaped pipe in cell, got:\n%s", body)
	}
}
