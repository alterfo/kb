package sqlddl

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/importer"
)

func init() {
	importer.Register(".sql", func() importer.FileImporter { return New() })
}

type Importer struct{}

func New() *Importer { return &Importer{} }

func (i *Importer) Ext() string { return ".sql" }

type column struct {
	Name       string
	Type       string
	NotNull    bool
	PrimaryKey bool
	References string
}

type table struct {
	Name        string
	Columns     []column
	PrimaryKey  []string
	ForeignKeys []string
}

func (i *Importer) Import(path string) ([]connector.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sqlddl: read %s: %w", path, err)
	}

	name := filepath.Base(path)
	var updatedAt time.Time
	if info, err := os.Stat(path); err == nil {
		updatedAt = info.ModTime()
	}

	tables := parseCreateTables(string(data))
	docs := make([]connector.Document, 0, len(tables))
	for _, tbl := range tables {
		docs = append(docs, connector.Document{
			ID:        path + "#" + tbl.Name,
			Kind:      "sql-table",
			Title:     tbl.Name,
			UpdatedAt: updatedAt,
			Body:      renderTable(tbl),
			Frontmatter: map[string]any{
				"file_path":    path,
				"file_name":    name,
				"table":        tbl.Name,
				"primary_key":  tbl.PrimaryKey,
				"foreign_keys": tbl.ForeignKeys,
			},
		})
	}
	return docs, nil
}

var (
	createTableRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([\w."` + "`" + `]+)\s*\((.*)\)\s*$`)
	blockComment  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineComment   = regexp.MustCompile(`--[^\n]*`)
)

func parseCreateTables(src string) []table {
	src = blockComment.ReplaceAllString(src, "")
	src = lineComment.ReplaceAllString(src, "")

	var tables []table
	for _, stmt := range splitStatements(src) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		m := createTableRe.FindStringSubmatch(stmt)
		if m == nil {
			continue
		}
		tables = append(tables, parseTableBody(unquoteIdent(m[1]), m[2]))
	}
	return tables
}

func splitStatements(src string) []string {
	var stmts []string
	depth := 0
	start := 0
	for i, r := range src {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ';':
			if depth == 0 {
				stmts = append(stmts, src[start:i])
				start = i + 1
			}
		}
	}
	if start < len(src) {
		stmts = append(stmts, src[start:])
	}
	return stmts
}

func parseTableBody(name, body string) table {
	tbl := table{Name: name}
	for _, item := range splitTopLevel(body) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		upper := strings.ToUpper(item)
		switch {
		case strings.HasPrefix(upper, "PRIMARY KEY"):
			tbl.PrimaryKey = append(tbl.PrimaryKey, extractParenColumns(item)...)
		case strings.HasPrefix(upper, "FOREIGN KEY"):
			if fk, ok := parseForeignKeyClause(item); ok {
				tbl.ForeignKeys = append(tbl.ForeignKeys, fk)
			}
		case strings.HasPrefix(upper, "UNIQUE"), strings.HasPrefix(upper, "CHECK"), strings.HasPrefix(upper, "CONSTRAINT"):
			// table-level constraints we don't model explicitly beyond PK/FK
		default:
			col := parseColumn(item)
			tbl.Columns = append(tbl.Columns, col)
			if col.PrimaryKey {
				tbl.PrimaryKey = append(tbl.PrimaryKey, col.Name)
			}
			if col.References != "" {
				tbl.ForeignKeys = append(tbl.ForeignKeys, col.Name+" -> "+col.References)
			}
		}
	}
	return tbl
}

func splitTopLevel(body string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		}
	}
	if start < len(body) {
		parts = append(parts, body[start:])
	}
	return parts
}

var referencesRe = regexp.MustCompile(`(?is)REFERENCES\s+([\w."` + "`" + `]+)\s*\(([^)]*)\)`)

func parseColumn(item string) column {
	fields := strings.Fields(item)
	if len(fields) < 2 {
		return column{Name: unquoteIdent(strings.TrimSpace(item))}
	}
	col := column{Name: unquoteIdent(fields[0])}
	col.Type = collectType(item, fields)

	upper := strings.ToUpper(item)
	if strings.Contains(upper, "NOT NULL") {
		col.NotNull = true
	}
	if strings.Contains(upper, "PRIMARY KEY") {
		col.PrimaryKey = true
	}
	if m := referencesRe.FindStringSubmatch(item); m != nil {
		refTable := unquoteIdent(strings.TrimSpace(m[1]))
		refCol := unquoteIdent(strings.TrimSpace(m[2]))
		col.References = refTable + "(" + refCol + ")"
	}
	return col
}

func collectType(item string, fields []string) string {
	rawType := fields[1]
	rest := item[strings.Index(item, fields[1])+len(fields[1]):]
	rest = strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(rest, "(") {
		depth := 0
		for i, r := range rest {
			if r == '(' {
				depth++
			} else if r == ')' {
				depth--
				if depth == 0 {
					rawType += rest[:i+1]
					break
				}
			}
		}
	}
	return rawType
}

func extractParenColumns(item string) []string {
	open := strings.Index(item, "(")
	closeIdx := strings.LastIndex(item, ")")
	if open == -1 || closeIdx == -1 || closeIdx <= open {
		return nil
	}
	inner := item[open+1 : closeIdx]
	var cols []string
	for _, c := range strings.Split(inner, ",") {
		c = unquoteIdent(strings.TrimSpace(c))
		if c != "" {
			cols = append(cols, c)
		}
	}
	return cols
}

func parseForeignKeyClause(item string) (string, bool) {
	cols := extractParenColumns(item[:strings.Index(strings.ToUpper(item), "REFERENCES")])
	m := referencesRe.FindStringSubmatch(item)
	if m == nil || len(cols) == 0 {
		return "", false
	}
	refTable := unquoteIdent(strings.TrimSpace(m[1]))
	refCols := strings.Split(m[2], ",")
	for i := range refCols {
		refCols[i] = unquoteIdent(strings.TrimSpace(refCols[i]))
	}
	return strings.Join(cols, ",") + " -> " + refTable + "(" + strings.Join(refCols, ",") + ")", true
}

func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"")
	return s
}

func renderTable(tbl table) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", tbl.Name)
	b.WriteString("| Column | Type | Not Null | Primary Key | References |\n")
	b.WriteString("|---|---|---|---|---|\n")
	pk := make(map[string]bool, len(tbl.PrimaryKey))
	for _, c := range tbl.PrimaryKey {
		pk[c] = true
	}
	for _, c := range tbl.Columns {
		fmt.Fprintf(&b, "| %s | %s | %v | %v | %s |\n",
			c.Name, c.Type, c.NotNull, c.PrimaryKey || pk[c.Name], c.References)
	}
	if len(tbl.ForeignKeys) > 0 {
		b.WriteString("\nForeign keys:\n")
		for _, fk := range tbl.ForeignKeys {
			fmt.Fprintf(&b, "- %s\n", fk)
		}
	}
	return b.String()
}
