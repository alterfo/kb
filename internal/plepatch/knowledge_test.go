package plepatch

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
)

func knowledgeTokenizer(t *testing.T) *Tokenizer {
	t.Helper()
	tok, err := NewTokenizer(byteLevelVocab(), nil, false, false, "gpt2")
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	return tok
}

func TestParseKnowledgeDefaultsMerge(t *testing.T) {
	knowledge, err := ParseKnowledge([]byte(`{
		"defaults": {"at": "last", "heads": "all", "orders": "all", "note": "base", "prefix": "ctx"},
		"entries": [
			{"trigger": "a", "op": "zero"},
			{"trigger": "b", "op": "zero", "at": "first", "heads": [0, 1], "orders": [2], "note": "n"}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseKnowledge: %v", err)
	}
	if knowledge.Defaults.At != "last" || knowledge.Defaults.Note != "base" || knowledge.Defaults.Prefix != "ctx" {
		t.Fatalf("defaults not parsed: %+v", knowledge.Defaults)
	}
	if knowledge.Defaults.Heads != nil || knowledge.Defaults.Orders != nil {
		t.Fatalf("all defaults should remain nil: %+v", knowledge.Defaults)
	}
	if len(knowledge.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(knowledge.Entries))
	}
	first := knowledge.Entries[0]
	if first.At != "last" || first.Note != "base" || first.Prefix != "ctx" {
		t.Fatalf("first entry did not inherit defaults: %+v", first)
	}
	second := knowledge.Entries[1]
	if second.At != "first" || second.Note != "n" {
		t.Fatalf("second entry did not override: %+v", second)
	}
	if !slices.Equal(second.Heads, []int{0, 1}) || !slices.Equal(second.Orders, []int{2}) {
		t.Fatalf("second entry filters wrong: %+v", second)
	}
}

func TestParseKnowledgeErrors(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{"missing trigger", `{"entries":[{"op":"zero"}]}`},
		{"unknown op", `{"entries":[{"trigger":"a","op":"scale"}]}`},
		{"set without vector", `{"entries":[{"trigger":"a","op":"set"}]}`},
		{"copy_from without source", `{"entries":[{"trigger":"a","op":"copy_from"}]}`},
		{"invalid at", `{"entries":[{"trigger":"a","op":"zero","at":"middle"}]}`},
		{"invalid heads", `{"entries":[{"trigger":"a","op":"zero","heads":"few"}]}`},
		{"invalid orders", `{"entries":[{"trigger":"a","op":"zero","orders":"second"}]}`},
	}
	for _, tc := range cases {
		if _, err := ParseKnowledge([]byte(tc.doc)); err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

func TestBuildPlanPositions(t *testing.T) {
	c := loadValid(t)
	tok := knowledgeTokenizer(t)

	tokens := []int64{97, 98}
	seqRows := c.RowsForSequence(tokens)
	headRows := func(pos int) []uint64 {
		return rowsForHeads(seqRows[pos], []int{0, 1})
	}

	base := `{"entries":[{"trigger":"ab","op":"zero","at":%s,"heads":[0,1],"orders":[2]}]}`

	lastDoc := strings.Replace(base, "%s", `"last"`, 1)
	plan, err := ParseKnowledge([]byte(lastDoc))
	if err != nil {
		t.Fatalf("ParseKnowledge last: %v", err)
	}
	built, err := plan.BuildPlan(c, tok, 160, nil)
	if err != nil {
		t.Fatalf("BuildPlan last: %v", err)
	}
	if !slices.Equal(built.Entries[0].Rows, headRows(1)) {
		t.Fatalf("last rows = %v, want %v", built.Entries[0].Rows, headRows(1))
	}

	firstDoc := strings.Replace(base, "%s", `"first"`, 1)
	plan, _ = ParseKnowledge([]byte(firstDoc))
	built, err = plan.BuildPlan(c, tok, 160, nil)
	if err != nil {
		t.Fatalf("BuildPlan first: %v", err)
	}
	if !slices.Equal(built.Entries[0].Rows, headRows(0)) {
		t.Fatalf("first rows = %v, want %v", built.Entries[0].Rows, headRows(0))
	}

	intDoc := strings.Replace(base, "%s", `1`, 1)
	plan, _ = ParseKnowledge([]byte(intDoc))
	built, err = plan.BuildPlan(c, tok, 160, nil)
	if err != nil {
		t.Fatalf("BuildPlan int: %v", err)
	}
	if !slices.Equal(built.Entries[0].Rows, headRows(1)) {
		t.Fatalf("int rows = %v, want %v", built.Entries[0].Rows, headRows(1))
	}

	allDoc := strings.Replace(base, "%s", `"all"`, 1)
	plan, _ = ParseKnowledge([]byte(allDoc))
	built, err = plan.BuildPlan(c, tok, 160, nil)
	if err != nil {
		t.Fatalf("BuildPlan all: %v", err)
	}
	wantAll := append(append([]uint64(nil), headRows(0)...), headRows(1)...)
	if !slices.Equal(built.Entries[0].Rows, wantAll) {
		t.Fatalf("all rows = %v, want %v", built.Entries[0].Rows, wantAll)
	}
}

func TestBuildPlanCopyFromMean(t *testing.T) {
	c := loadValid(t)
	tok := knowledgeTokenizer(t)

	knowledge, err := ParseKnowledge([]byte(`{
		"entries": [{"trigger": "a", "op": "copy_from", "copy_from": "bc", "at": "last", "heads": [0, 1], "orders": [2]}]
	}`))
	if err != nil {
		t.Fatalf("ParseKnowledge: %v", err)
	}

	readRow := func(row uint64) ([]float32, error) {
		vec := make([]float32, 160)
		for i := range vec {
			vec[i] = float32(row) * float32(i+1)
		}
		return vec, nil
	}

	plan, err := knowledge.BuildPlan(c, tok, 160, readRow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	sourceRows := rowsForHeads(c.RowsForSequence([]int64{98, 99})[1], []int{0, 1})
	targetRows := rowsForHeads(c.RowsForSequence([]int64{97})[0], []int{0, 1})

	expected := make([]float32, 160)
	for i := range expected {
		var sum float64
		for _, row := range sourceRows {
			sum += float64(readRowVec(t, readRow, row)[i])
		}
		expected[i] = float32(sum / float64(len(sourceRows)))
	}

	if len(plan.RowOps) != len(targetRows) {
		t.Fatalf("RowOps = %d, want %d", len(plan.RowOps), len(targetRows))
	}
	for _, row := range targetRows {
		got := plan.RowOps[row]
		if len(got) != 160 {
			t.Fatalf("row %d vector length = %d, want 160", row, len(got))
		}
		for i := range got {
			if math.Abs(float64(got[i]-expected[i])) > 1e-6 {
				t.Fatalf("row %d component %d = %v, want %v", row, i, got[i], expected[i])
			}
		}
	}
	if !slices.Equal(plan.Entries[0].Rows, targetRows) {
		t.Fatalf("entry rows = %v, want %v", plan.Entries[0].Rows, targetRows)
	}
}

func readRowVec(t *testing.T, readRow RowReader, row uint64) []float32 {
	t.Helper()
	vec, err := readRow(row)
	if err != nil {
		t.Fatalf("readRow(%d): %v", row, err)
	}
	return vec
}

func TestBuildPlanRandomDeterminism(t *testing.T) {
	c := loadValid(t)
	tok := knowledgeTokenizer(t)

	doc := `{
		"entries": [
			{"trigger": "a", "op": "random", "seed": 1, "heads": [0], "orders": [2]},
			{"trigger": "b", "op": "random", "seed": 1, "heads": [0], "orders": [2]}
		]
	}`
	knowledge, err := ParseKnowledge([]byte(doc))
	if err != nil {
		t.Fatalf("ParseKnowledge: %v", err)
	}

	first, err := knowledge.BuildPlan(c, tok, 160, nil)
	if err != nil {
		t.Fatalf("BuildPlan first: %v", err)
	}
	second, err := knowledge.BuildPlan(c, tok, 160, nil)
	if err != nil {
		t.Fatalf("BuildPlan second: %v", err)
	}

	if !slices.Equal(first.Entries[0].Vector, first.Entries[1].Vector) {
		t.Fatalf("same seed produced different vectors across entries")
	}
	if !slices.Equal(first.Entries[0].Vector, second.Entries[0].Vector) {
		t.Fatalf("same seed produced different vectors across runs")
	}
}

func TestBuildPlanCollisions(t *testing.T) {
	c := loadValid(t)
	tok := knowledgeTokenizer(t)

	knowledge, err := ParseKnowledge([]byte(`{
		"entries": [
			{"trigger": "a", "op": "zero", "heads": [0], "orders": [2]},
			{"trigger": "a", "op": "zero", "heads": [0], "orders": [2]}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseKnowledge: %v", err)
	}

	plan, err := knowledge.BuildPlan(c, tok, 160, nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.UniqueRows() != 1 {
		t.Fatalf("UniqueRows = %d, want 1", plan.UniqueRows())
	}
	if plan.Touched != 2 {
		t.Fatalf("Touched = %d, want 2", plan.Touched)
	}
	if plan.Collisions != 1 {
		t.Fatalf("Collisions = %d, want 1", plan.Collisions)
	}
	if !slices.Equal(plan.Entries[0].Rows, plan.Entries[1].Rows) {
		t.Fatalf("colliding entries should target same rows")
	}
}

func TestBuildPlanValidation(t *testing.T) {
	c := loadValid(t)
	tok := knowledgeTokenizer(t)

	cases := []struct {
		name string
		doc  string
	}{
		{"head out of range", `{"entries":[{"trigger":"a","op":"zero","heads":[16]}]}`},
		{"order out of range", `{"entries":[{"trigger":"a","op":"zero","orders":[1]}]}`},
		{"at out of range", `{"entries":[{"trigger":"a","op":"zero","at":5}]}`},
		{"inline vector wrong length", `{"entries":[{"trigger":"a","op":"set","vector":[1,2,3]}]}`},
		{"empty selected heads", `{"entries":[{"trigger":"a","op":"zero","heads":[0],"orders":[3]}]}`},
	}
	for _, tc := range cases {
		knowledge, err := ParseKnowledge([]byte(tc.doc))
		if err != nil {
			t.Fatalf("%s: ParseKnowledge: %v", tc.name, err)
		}
		if _, err := knowledge.BuildPlan(c, tok, 160, nil); err == nil {
			t.Fatalf("%s: expected BuildPlan error", tc.name)
		}
	}
}

func TestSetInlineVector(t *testing.T) {
	c := loadValid(t)
	tok := knowledgeTokenizer(t)

	values := make([]string, 160)
	for i := range values {
		values[i] = "0.5"
	}
	vecJSON := "[" + strings.Join(values, ",") + "]"
	doc := `{"entries":[{"trigger":"a","op":"set","vector":` + vecJSON + `,"heads":[0],"orders":[2]}]}`
	knowledge, err := ParseKnowledge([]byte(doc))
	if err != nil {
		t.Fatalf("ParseKnowledge: %v", err)
	}
	plan, err := knowledge.BuildPlan(c, tok, 160, nil)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Entries[0].Vector) != 160 {
		t.Fatalf("vector length = %d, want 160", len(plan.Entries[0].Vector))
	}
	for _, v := range plan.Entries[0].Vector {
		if v != 0.5 {
			t.Fatalf("vector element = %v, want 0.5", v)
		}
	}
	if len(plan.RowOps) != 1 {
		t.Fatalf("RowOps = %d, want 1", len(plan.RowOps))
	}
}

func TestKnowledgeJSONRoundTripSeeds(t *testing.T) {
	var raw struct {
		Entries []struct {
			Seed *int64 `json:"seed"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(`{"entries":[{"trigger":"a","op":"random","seed":7}]}`), &raw); err != nil {
		t.Fatalf("unmarshal seeds: %v", err)
	}
	if raw.Entries[0].Seed == nil || *raw.Entries[0].Seed != 7 {
		t.Fatalf("seed pointer not decoded: %+v", raw.Entries[0].Seed)
	}
}
