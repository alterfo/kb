package sqlddl

import (
	"os"
	"strings"
	"testing"
)

func TestExt(t *testing.T) {
	if got := New().Ext(); got != ".sql" {
		t.Fatalf("Ext() = %q, want .sql", got)
	}
}

func TestImport_MultiTablePKAndFK(t *testing.T) {
	docs, err := New().Import("testdata/sample.sql")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 table documents, got %d", len(docs))
	}

	byTable := map[string]int{}
	for i, d := range docs {
		byTable[d.Title] = i
		if d.Kind != "sql-table" {
			t.Errorf("Kind = %q, want sql-table", d.Kind)
		}
	}
	if _, ok := byTable["users"]; !ok {
		t.Fatal("expected users table")
	}
	if _, ok := byTable["posts"]; !ok {
		t.Fatal("expected posts table (IF NOT EXISTS variant)")
	}

	users := docs[byTable["users"]]
	pk, ok := users.Frontmatter["primary_key"].([]string)
	if !ok || len(pk) != 1 || pk[0] != "id" {
		t.Errorf("users primary_key = %v", users.Frontmatter["primary_key"])
	}
	if !strings.Contains(users.Body, "email") || !strings.Contains(users.Body, "VARCHAR(255)") {
		t.Errorf("users body missing column info:\n%s", users.Body)
	}

	posts := docs[byTable["posts"]]
	postsPK, _ := posts.Frontmatter["primary_key"].([]string)
	if len(postsPK) != 1 || postsPK[0] != "id" {
		t.Errorf("posts primary_key = %v", posts.Frontmatter["primary_key"])
	}
	fks, ok := posts.Frontmatter["foreign_keys"].([]string)
	if !ok || len(fks) == 0 {
		t.Fatalf("posts foreign_keys = %v", posts.Frontmatter["foreign_keys"])
	}
	found := false
	for _, fk := range fks {
		if strings.Contains(fk, "author_id") && strings.Contains(fk, "users(id)") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected author_id -> users(id) fk, got %v", fks)
	}
}

func TestImport_IgnoresNonCreateTableStatements(t *testing.T) {
	docs, err := New().Import("testdata/sample.sql")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, d := range docs {
		if strings.Contains(strings.ToUpper(d.Body), "INSERT INTO") {
			t.Errorf("unexpected INSERT leaking into body: %s", d.Body)
		}
	}
}

func TestImport_MissingFile(t *testing.T) {
	if _, err := New().Import("testdata/does-not-exist.sql"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestImport_NoCreateTableYieldsNoDocuments(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/empty.sql"
	if err := os.WriteFile(path, []byte("-- nothing here\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	docs, err := New().Import(path)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 documents, got %d", len(docs))
	}
}

func TestParseCreateTables_InlinePrimaryKey(t *testing.T) {
	tables := parseCreateTables(`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT NOT NULL);`)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	tbl := tables[0]
	if len(tbl.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(tbl.Columns))
	}
	if !tbl.Columns[0].PrimaryKey {
		t.Errorf("expected id column to be marked PRIMARY KEY")
	}
	if !tbl.Columns[1].NotNull {
		t.Errorf("expected name column to be marked NOT NULL")
	}
}
