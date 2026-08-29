package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

type panicChat struct{}

func (panicChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	panic("code extraction path must not call the LLM")
}

func newCodeUpdater(t *testing.T) (*GraphUpdater, graphstore.Store) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := sqlite.NewGraphStore(db)
	return NewGraphUpdater(store, nil, nil), store
}

const calcSource = `package calc

import "fmt"

type Calc interface {
	Add(a, b int) int
}

type IntCalc struct{}

func (c *IntCalc) Add(a, b int) int {
	return c.round(a + b)
}

func (c *IntCalc) round(n int) int {
	return n
}

func Sum(nums []int) int {
	total := 0
	for _, n := range nums {
		total = Add(total, n)
	}
	return total
}

func Add(a, b int) int {
	return a + b
}

func Report(tag string) string {
	return fmt.Sprintf("sum=%s", tag)
}
`

func codeChunks() []vector.Chunk {
	return []vector.Chunk{{
		ID:       "code/calc.go#0",
		RefDocID: "code/calc.go",
		Text:     calcSource,
		FilePath: "code/calc.go",
		Metadata: map[string]string{"kind": "code"},
	}}
}

func TestUpdateDocumentCodePathSkipsLLM(t *testing.T) {
	updater, store := newCodeUpdater(t)
	ctx := context.Background()

	if _, err := updater.UpdateDocument(ctx, "code/calc.go", codeChunks()); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) == 0 {
		t.Fatal("expected code entities")
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) == 0 {
		t.Fatal("expected code relations")
	}

	byName := map[string]graphstore.Entity{}
	idToName := map[string]string{}
	for _, e := range entities {
		byName[e.Name] = e
		idToName[e.ID] = e.Name
	}
	for _, want := range []struct{ name, typ string }{
		{"calc", "code-package"},
		{"Calc", "code-type"},
		{"IntCalc", "code-type"},
		{"Sum", "code-function"},
		{"Add", "code-function"},
		{"Report", "code-function"},
		{"IntCalc.Add", "code-method"},
		{"IntCalc.round", "code-method"},
		{"fmt", "code-package"},
		{"fmt.Sprintf", "code-function"},
	} {
		e, ok := byName[want.name]
		if !ok {
			t.Errorf("missing entity %q", want.name)
			continue
		}
		if e.Type != want.typ {
			t.Errorf("entity %q type = %q, want %q", want.name, e.Type, want.typ)
		}
	}

	type edge struct{ src, dst, typ string }
	wantEdges := []edge{
		{"calc", "Add", "DECLARES"},
		{"calc", "Sum", "DECLARES"},
		{"calc", "Report", "DECLARES"},
		{"calc", "Calc", "DECLARES"},
		{"calc", "IntCalc", "DECLARES"},
		{"IntCalc", "IntCalc.Add", "DECLARES"},
		{"IntCalc", "IntCalc.round", "DECLARES"},
		{"calc", "fmt", "IMPORTS"},
		{"Sum", "Add", "CALLS"},
		{"IntCalc.Add", "IntCalc.round", "CALLS"},
		{"IntCalc", "Calc", "IMPLEMENTS"},
	}
	haveEdges := map[edge]bool{}
	for _, r := range relations {
		srcName := idToName[r.Src]
		dstName := idToName[r.Dst]
		haveEdges[edge{srcName, dstName, r.Type}] = true
	}
	for _, w := range wantEdges {
		if !haveEdges[w] {
			t.Errorf("missing edge %+v; have %+v", w, haveEdges)
		}
	}
	for _, r := range relations {
		if r.Provenance != provenanceGoCode || r.Confidence != 1.0 {
			t.Fatalf("code relation %+v: want provenance %q and confidence 1.0", r, provenanceGoCode)
		}
	}
}

func TestUpdateDocumentCodeReindexNoDuplicates(t *testing.T) {
	updater, store := newCodeUpdater(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := updater.UpdateDocument(ctx, "code/calc.go", codeChunks()); err != nil {
			t.Fatalf("UpdateDocument[%d]: %v", i, err)
		}
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(entities) != 10 {
		t.Fatalf("got %d entities after reindex, want 10 (no duplicates)", len(entities))
	}
	if len(relations) != 12 {
		t.Fatalf("got %d relations after reindex, want 12 (no duplicates)", len(relations))
	}
}

func TestUpdateDocumentCodeBrokenSourceFailOpen(t *testing.T) {
	updater, store := newCodeUpdater(t)
	ctx := context.Background()

	chunks := []vector.Chunk{{
		ID:       "code/broken.go#0",
		RefDocID: "code/broken.go",
		Text:     "package broken\n\nfunc oops( {\n",
		FilePath: "code/broken.go",
		Metadata: map[string]string{"kind": "code"},
	}}
	if _, err := updater.UpdateDocument(ctx, "code/broken.go", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(entities) != 0 || len(relations) != 0 {
		t.Fatalf("expected empty graph for broken source, got %d entities, %d relations", len(entities), len(relations))
	}
}

func TestUpdateDocumentCodeNonCodeUnchanged(t *testing.T) {
	updater, store := newCodeUpdater(t)
	ctx := context.Background()

	chunks := []vector.Chunk{{
		ID:       "notes/doc1#0",
		RefDocID: "notes/doc1",
		Text:     "Alice knows Bob.",
		FilePath: "notes/doc1.md",
		Metadata: map[string]string{"kind": "note"},
	}}
	if _, err := updater.UpdateDocument(ctx, "notes/doc1", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("non-code chunk with nil extractor should contribute nothing, got %d entities", len(entities))
	}
}

func TestUpdateDocumentRoutesGoFileByExtension(t *testing.T) {
	updater, store := newCodeUpdater(t)
	ctx := context.Background()

	// A .go file fetched through a connector that does not set kind "code"
	// (e.g. GitHub content) must still take the deterministic code-graph
	// path instead of generic LLM extraction.
	chunks := []vector.Chunk{{
		ID:       "src/main.go#0",
		RefDocID: "src/main.go",
		Text:     "package main\n\nfunc main() {}\n",
		FilePath: "src/main.go",
		FileName: "main.go",
		Metadata: map[string]string{"kind": "content"},
	}}
	if _, err := updater.UpdateDocument(ctx, "src/main.go", chunks); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}
	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) == 0 {
		t.Fatal("expected code entities for a .go file with kind=content")
	}
}

func TestUpdateDocumentRoutesGoFileByMetadata(t *testing.T) {
	updater, store := newCodeUpdater(t)
	ctx := context.Background()

	cases := []map[string]string{
		{"kind": "content", "path": "cmd/kb/main.go"},
		{"kind": "content", "id": "owner/repo:contents:cmd/kb/main.go"},
	}
	for _, md := range cases {
		chunks := []vector.Chunk{{
			ID:       "github-owner-repo#0",
			RefDocID: "github-owner-repo",
			Text:     "package main\n\nfunc main() {}\n",
			FilePath: "github-owner-repo.md",
			FileName: "owner-repo.md",
			Metadata: md,
		}}
		if _, err := updater.UpdateDocument(ctx, "github-owner-repo", chunks); err != nil {
			t.Fatalf("UpdateDocument(%v): %v", md, err)
		}
		entities, err := store.AllEntities(ctx)
		if err != nil {
			t.Fatalf("AllEntities(%v): %v", md, err)
		}
		if len(entities) == 0 {
			t.Errorf("expected code entities for metadata %v", md)
		}
	}
}

func TestUpdateDocumentCodePackageGathersSiblingFiles(t *testing.T) {
	root := t.TempDir()
	writeCorpus := func(name, body string) {
		t.Helper()
		doc := connector.Document{ID: name, Source: "code", Kind: "code", Title: name, Body: body}
		rendered, err := render.Render(doc)
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		path := filepath.Join(root, "code", name+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	writeCorpus("a.go", `package calc

type Adder interface {
	Add(a, b int) int
}

func Add(a, b int) int { return a + b }
`)
	writeCorpus("b.go", `package calc

type IntAdder struct{}

func (IntAdder) Add(a, b int) int { return a + b }

func Sum(nums []int) int {
	total := 0
	for _, n := range nums {
		total = Add(total, n)
	}
	return total
}
`)

	updater, store := newCodeUpdater(t)
	updater.CodeRoot = root
	ctx := context.Background()

	chunk := codeChunks()
	chunk[0].ID = "code/a.go#0"
	chunk[0].RefDocID = "code/a.go"
	chunk[0].FilePath = "code/a.go.md"
	chunk[0].Text = `package calc

type Adder interface {
	Add(a, b int) int
}

func Add(a, b int) int { return a + b }
`
	if _, err := updater.UpdateDocument(ctx, "code/a.go", chunk); err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	var fnSum, fnAdd graphstore.Entity
	for _, e := range entities {
		switch e.Name {
		case "Sum":
			fnSum = e
		case "Add":
			fnAdd = e
		}
	}
	if fnSum.ID == "" || fnAdd.ID == "" {
		t.Fatalf("sibling entities missing, got %+v", entities)
	}
	relations, err := store.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	found := false
	for _, r := range relations {
		if r.Type == "CALLS" && r.Src == fnSum.ID && r.Dst == fnAdd.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("cross-file CALLS Sum->Add missing, relations=%+v", relations)
	}
}

func TestUpdateDocumentCodeDistinctPackagesSameSourceDoNotCollide(t *testing.T) {
	updater, store := newCodeUpdater(t)
	ctx := context.Background()

	alpha := `package alpha

func Parse(s string) string { return s }
`
	beta := `package beta

func Parse(s string) int { return len(s) }
`
	chunks := []vector.Chunk{
		{
			ID: "code/a.md#0", RefDocID: "code/a.md", Text: alpha,
			FilePath: "code/a.md", Metadata: map[string]string{"kind": "code", "id": "internal/alpha/alpha.go"},
		},
		{
			ID: "code/b.md#0", RefDocID: "code/b.md", Text: beta,
			FilePath: "code/b.md", Metadata: map[string]string{"kind": "code", "id": "internal/beta/beta.go"},
		},
	}
	for _, c := range chunks {
		if _, err := updater.UpdateDocument(ctx, c.RefDocID, []vector.Chunk{c}); err != nil {
			t.Fatalf("UpdateDocument %s: %v", c.ID, err)
		}
	}

	entities, err := store.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	var parses []graphstore.Entity
	for _, e := range entities {
		if e.Name == "Parse" {
			parses = append(parses, e)
		}
	}
	if len(parses) != 2 {
		t.Fatalf("got %d Parse entities, want 2 (one per package): %+v", len(parses), entities)
	}
	if parses[0].ID == parses[1].ID {
		t.Fatalf("Parse entities from distinct packages collided: %s", parses[0].ID)
	}
	if !strings.Contains(parses[0].ID, "internal-alpha") || !strings.Contains(parses[1].ID, "internal-beta") {
		t.Fatalf("Parse entities not qualified by real package dir: %s, %s", parses[0].ID, parses[1].ID)
	}
}
