package codegraph

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alterfo/kb/internal/store/graphstore"
)

func loadFixture(t *testing.T, name string) (string, []byte) {
	t.Helper()
	path := filepath.Join("testdata", "codegraph", name)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return path, src
}

func findEntity(t *testing.T, entities []graphstore.Entity, name, typ string) graphstore.Entity {
	t.Helper()
	for _, e := range entities {
		if e.Name == name && e.Type == typ {
			return e
		}
	}
	t.Fatalf("entity %s/%s not found; have %+v", name, typ, entities)
	return graphstore.Entity{}
}

func findRelation(t *testing.T, relations []graphstore.Relation, src, dst, typ string) graphstore.Relation {
	t.Helper()
	for _, r := range relations {
		if r.Src == src && r.Dst == dst && r.Type == typ {
			return r
		}
	}
	t.Fatalf("relation %s->%s (%s) not found; have %+v", src, dst, typ, relations)
	return graphstore.Relation{}
}

func TestExtractCalcGolden(t *testing.T) {
	path, src := loadFixture(t, "calc.go")
	entities, relations, err := Extract(path, src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	pkg := findEntity(t, entities, "calc", TypePackage)
	fnAdd := findEntity(t, entities, "Add", TypeFunction)
	fnReport := findEntity(t, entities, "Report", TypeFunction)
	fnSum := findEntity(t, entities, "Sum", TypeFunction)
	typCalc := findEntity(t, entities, "Calculator", TypeType)
	typInt := findEntity(t, entities, "IntCalc", TypeType)
	mAdd := findEntity(t, entities, "IntCalc.Add", TypeMethod)
	mRound := findEntity(t, entities, "IntCalc.round", TypeMethod)
	findEntity(t, entities, "fmt", TypePackage)
	findEntity(t, entities, "strings", TypePackage)
	findEntity(t, entities, "fmt.Sprintf", TypeFunction)
	findEntity(t, entities, "strings.TrimSpace", TypeFunction)

	findRelation(t, relations, pkg.ID, typCalc.ID, EdgeDeclares)
	findRelation(t, relations, pkg.ID, typInt.ID, EdgeDeclares)
	findRelation(t, relations, pkg.ID, fnAdd.ID, EdgeDeclares)
	findRelation(t, relations, pkg.ID, fnReport.ID, EdgeDeclares)
	findRelation(t, relations, pkg.ID, fnSum.ID, EdgeDeclares)
	findRelation(t, relations, typInt.ID, mAdd.ID, EdgeDeclares)
	findRelation(t, relations, typInt.ID, mRound.ID, EdgeDeclares)

	findRelation(t, relations, pkg.ID, entityID("fmt", TypePackage), EdgeImports)
	findRelation(t, relations, pkg.ID, entityID("strings", TypePackage), EdgeImports)

	findRelation(t, relations, fnSum.ID, fnAdd.ID, EdgeCalls)
	findRelation(t, relations, mAdd.ID, mRound.ID, EdgeCalls)
	findRelation(t, relations, fnReport.ID, entityID("fmt.Sprintf", TypeFunction), EdgeCalls)
	findRelation(t, relations, fnReport.ID, entityID("strings.TrimSpace", TypeFunction), EdgeCalls)

	findRelation(t, relations, typInt.ID, typCalc.ID, EdgeImplements)
}

func TestExtractCalcDeterministic(t *testing.T) {
	path, src := loadFixture(t, "calc.go")
	e1, r1, err := Extract(path, src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	e2, r2, err := Extract(path, src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(e1) != len(e2) || len(r1) != len(r2) {
		t.Fatalf("nondeterministic extraction: %d/%d vs %d/%d", len(e1), len(e2), len(r1), len(r2))
	}
	for i := range e1 {
		if !reflect.DeepEqual(e1[i], e2[i]) {
			t.Fatalf("entity mismatch at %d: %+v vs %+v", i, e1[i], e2[i])
		}
	}
	for i := range r1 {
		if !reflect.DeepEqual(r1[i], r2[i]) {
			t.Fatalf("relation mismatch at %d: %+v vs %+v", i, r1[i], r2[i])
		}
	}
}

func TestExtractShapesSyntacticFallback(t *testing.T) {
	path, src := loadFixture(t, "shapes.go")
	entities, relations, err := Extract(path, src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	pkg := findEntity(t, entities, "shapes", TypePackage)
	fnMake := findEntity(t, entities, "Make", TypeFunction)
	fnNew := findEntity(t, entities, "NewSquare", TypeFunction)
	typSquare := findEntity(t, entities, "Square", TypeType)
	findEntity(t, entities, "Square.Area", TypeMethod)
	findEntity(t, entities, "example.com/nonexistent", TypePackage)

	findRelation(t, relations, pkg.ID, entityID("example.com/nonexistent", TypePackage), EdgeImports)
	findRelation(t, relations, fnMake.ID, fnNew.ID, EdgeCalls)
	findRelation(t, relations, typSquare.ID, findEntity(t, entities, "Square.Area", TypeMethod).ID, EdgeDeclares)

	for _, r := range relations {
		if r.Type == EdgeImplements {
			t.Fatalf("expected no IMPLEMENTS edges without type info, got %+v", r)
		}
	}
}

func TestExtractBrokenSyntaxFailOpen(t *testing.T) {
	path, src := loadFixture(t, "broken.go.txt")
	entities, relations, err := Extract(path, src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(entities) != 0 || len(relations) != 0 {
		t.Fatalf("expected empty graph for broken file, got %d entities, %d relations", len(entities), len(relations))
	}
}

func TestExtractEmptySource(t *testing.T) {
	entities, relations, err := Extract("empty.go", []byte(""))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(entities) != 0 || len(relations) != 0 {
		t.Fatalf("expected empty graph, got %d entities, %d relations", len(entities), len(relations))
	}
}

func TestExtractCallWeightAccumulates(t *testing.T) {
	src := []byte(`package demo

func Twice(n int) int {
	return Add(n, Add(n, n))
}

func Add(a, b int) int {
	return a + b
}
`)
	entities, relations, err := Extract("demo.go", src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	srcFn := findEntity(t, entities, "Twice", TypeFunction)
	dstFn := findEntity(t, entities, "Add", TypeFunction)
	rel := findRelation(t, relations, srcFn.ID, dstFn.ID, EdgeCalls)
	if rel.Weight != 2 {
		t.Fatalf("CALLS weight = %v, want 2", rel.Weight)
	}
}

func TestExtractDistinctPackagesDoNotCollide(t *testing.T) {
	src := []byte(`package alpha

func Parse(s string) string { return s }
`)
	alphaEnts, _, err := Extract("internal/alpha/alpha.go", src)
	if err != nil {
		t.Fatalf("Extract(alpha): %v", err)
	}
	betaSrc := []byte(`package beta

func Parse(s string) int { return len(s) }
`)
	betaEnts, _, err := Extract("internal/beta/beta.go", betaSrc)
	if err != nil {
		t.Fatalf("Extract(beta): %v", err)
	}
	alphaParse := findEntity(t, alphaEnts, "Parse", TypeFunction)
	betaParse := findEntity(t, betaEnts, "Parse", TypeFunction)
	if alphaParse.ID == betaParse.ID {
		t.Fatalf("Parse in different packages collided: %s", alphaParse.ID)
	}
	alphaPkg := findEntity(t, alphaEnts, "alpha", TypePackage)
	betaPkg := findEntity(t, betaEnts, "beta", TypePackage)
	if alphaPkg.ID == betaPkg.ID {
		t.Fatalf("package entities with different names collided: %s", alphaPkg.ID)
	}
}

func TestExtractFilesResolvesCrossFileEdges(t *testing.T) {
	a := `package calc

type Adder interface {
	Add(a, b int) int
}

func Add(a, b int) int { return a + b }
`
	b := `package calc

type IntAdder struct{}

func (IntAdder) Add(a, b int) int { return a + b }

func Sum(nums []int) int {
	total := 0
	for _, n := range nums {
		total = Add(total, n)
	}
	return total
}
`
	entities, relations, err := ExtractFiles([]File{
		{Path: "calc/a.go", Src: []byte(a)},
		{Path: "calc/b.go", Src: []byte(b)},
	})
	if err != nil {
		t.Fatalf("ExtractFiles: %v", err)
	}

	fnAdd := findEntity(t, entities, "Add", TypeFunction)
	fnSum := findEntity(t, entities, "Sum", TypeFunction)
	typAdder := findEntity(t, entities, "Adder", TypeType)
	typInt := findEntity(t, entities, "IntAdder", TypeType)

	findRelation(t, relations, fnSum.ID, fnAdd.ID, EdgeCalls)
	findRelation(t, relations, typInt.ID, typAdder.ID, EdgeImplements)
}

func TestExtractSingleFileMissesCrossFileCalls(t *testing.T) {
	b := `package calc

func Sum(nums []int) int {
	total := 0
	for _, n := range nums {
		total = Add(total, n)
	}
	return total
}
`
	_, relations, err := Extract("calc/b.go", []byte(b))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, r := range relations {
		if r.Type == EdgeCalls {
			t.Fatalf("single-file extraction must not resolve the cross-file call, got %+v", r)
		}
	}
}
