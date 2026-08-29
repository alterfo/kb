package got

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/alterfo/kb/internal/llm"
)

func TestDecomposeNilChatFailsOpen(t *testing.T) {
	o := New(Config{})
	got := o.decompose(context.Background(), "original query")
	if len(got) != 1 || got[0].Query != "original query" {
		t.Fatalf("got %v", got)
	}
	if got[0].DependsOn != nil {
		t.Fatalf("got deps %v, want nil", got[0].DependsOn)
	}
}

func TestDecomposeChatErrorFailsOpen(t *testing.T) {
	o := New(Config{Chat: fakeChat{err: errors.New("boom")}})
	got := o.decompose(context.Background(), "q")
	if len(got) != 1 || got[0].Query != "q" {
		t.Fatalf("got %v", got)
	}
}

func TestDecomposeParsesLegacyArrayAndCaps(t *testing.T) {
	o := New(Config{
		Chat:        fakeChat{resp: llm.ChatResponse{Content: `["a","b","c","d","e","f"]`}},
		MaxSubgoals: 3,
	})
	got := o.decompose(context.Background(), "q")
	if len(got) != 3 || got[0].Query != "a" || got[2].Query != "c" {
		t.Fatalf("got %v", got)
	}
	for i := range got {
		if got[i].DependsOn != nil {
			t.Fatalf("spec[%d].DependsOn = %v, want nil (legacy)", i, got[i].DependsOn)
		}
	}
}

func TestDecomposeEmptyArrayFailsOpen(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: `[]`}}})
	got := o.decompose(context.Background(), "q")
	if len(got) != 1 || got[0].Query != "q" {
		t.Fatalf("got %v", got)
	}
}

func TestDecomposeGarbageFailsOpen(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: "not json"}}})
	got := o.decompose(context.Background(), "q")
	if len(got) != 1 || got[0].Query != "q" {
		t.Fatalf("got %v", got)
	}
}

func TestParseSubgoalSpecsNewFormat(t *testing.T) {
	specs := parseSubgoalSpecs(`[
		{"subquestion": "what is the price", "depends_on": [1]},
		{"subquestion": "what is the product"},
		{"subquestion": "how does it compare", "depends_on": [0, 1]}
	]`)
	if len(specs) != 3 {
		t.Fatalf("got %d specs, want 3: %v", len(specs), specs)
	}
	if specs[0].Query != "what is the price" {
		t.Fatalf("specs[0].Query = %q", specs[0].Query)
	}
	if !reflect.DeepEqual(specs[0].DependsOn, []string{"1"}) {
		t.Fatalf("specs[0].DependsOn = %v, want [1]", specs[0].DependsOn)
	}
	if specs[1].DependsOn != nil {
		t.Fatalf("specs[1].DependsOn = %v, want nil", specs[1].DependsOn)
	}
	if !reflect.DeepEqual(specs[2].DependsOn, []string{"0", "1"}) {
		t.Fatalf("specs[2].DependsOn = %v, want [0 1]", specs[2].DependsOn)
	}
}

func TestParseSubgoalSpecsOldFormat(t *testing.T) {
	specs := parseSubgoalSpecs(`["a","b"]`)
	if len(specs) != 2 || specs[0].Query != "a" || specs[1].Query != "b" {
		t.Fatalf("got %v", specs)
	}
	if specs[0].DependsOn != nil || specs[1].DependsOn != nil {
		t.Fatalf("legacy deps = %v / %v, want nil", specs[0].DependsOn, specs[1].DependsOn)
	}
}

func TestParseSubgoalSpecsGarbageReturnsNil(t *testing.T) {
	if got := parseSubgoalSpecs("not json"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestBuildSubgoalDAGDropsSelfAndOutOfRangeDeps(t *testing.T) {
	specs := []subgoalSpec{
		{Query: "a", DependsOn: []string{"0", "9", "missing", "1"}},
		{Query: "b"},
		{Query: "c"},
	}
	d := buildSubgoalDAG(specs)
	want := []string{"1"}
	if !reflect.DeepEqual(d.deps["0"], want) {
		t.Fatalf("deps[0] = %v, want %v", d.deps["0"], want)
	}
}

func TestBuildSubgoalDAGBreaksCycle(t *testing.T) {
	specs := []subgoalSpec{
		{Query: "a", DependsOn: []string{"1"}},
		{Query: "b", DependsOn: []string{"0"}},
	}
	d := buildSubgoalDAG(specs)
	order, err := d.topoSort()
	if err != nil {
		t.Fatalf("topoSort after breakCycles: %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("order = %v, want 2 nodes", order)
	}
	assertTopoOrder(t, d, order)
}

func TestBuildSubgoalDAGEmptySpecs(t *testing.T) {
	d := buildSubgoalDAG(nil)
	if len(d.nodes) != 0 {
		t.Fatalf("got %d nodes, want 0", len(d.nodes))
	}
	order, err := d.topoSort()
	if err != nil || len(order) != 0 {
		t.Fatalf("topoSort = %v, %v; want empty", order, err)
	}
}

func TestFindGapsNilChatFailsOpen(t *testing.T) {
	o := New(Config{})
	got := o.findGaps(context.Background(), "q", "draft")
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestFindGapsParsesArray(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: `["missing pricing", "missing dates"]`}}})
	got := o.findGaps(context.Background(), "q", "draft")
	if len(got) != 2 || got[0].Query != "missing pricing" || got[0].ReportedBy != -1 || got[1].ReportedBy != -1 {
		t.Fatalf("got %v", got)
	}
}

func TestFindGapsInvalidJSONFailsOpen(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: "not json"}}})
	got := o.findGaps(context.Background(), "q", "draft")
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestFindGapsParsesObjectFormat(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: `[{"subquestion":"missing pricing","reported_by":1},{"subquestion":"missing dates"}]`}}})
	got := o.findGaps(context.Background(), "q", "draft")
	if len(got) != 2 {
		t.Fatalf("got %d gaps, want 2: %v", len(got), got)
	}
	if got[0].Query != "missing pricing" || got[0].ReportedBy != 1 {
		t.Fatalf("got[0] = %+v, want Query=missing pricing ReportedBy=1", got[0])
	}
	if got[1].Query != "missing dates" || got[1].ReportedBy != -1 {
		t.Fatalf("got[1] = %+v, want Query=missing dates ReportedBy=-1", got[1])
	}
}

func TestParseGapSpecsFormats(t *testing.T) {
	t.Run("object with string index", func(t *testing.T) {
		gaps := parseGapSpecs(`[{"subquestion":"g1","reported_by":"2"}]`)
		if len(gaps) != 1 || gaps[0].Query != "g1" || gaps[0].ReportedBy != 2 {
			t.Fatalf("got %+v", gaps)
		}
	})
	t.Run("legacy string array", func(t *testing.T) {
		gaps := parseGapSpecs(`["a","b"]`)
		if len(gaps) != 2 || gaps[0].Query != "a" || gaps[0].ReportedBy != -1 || gaps[1].ReportedBy != -1 {
			t.Fatalf("got %+v", gaps)
		}
	})
	t.Run("garbage returns nil", func(t *testing.T) {
		if got := parseGapSpecs("not json"); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
	t.Run("empty array returns empty", func(t *testing.T) {
		if got := parseGapSpecs(`[]`); len(got) != 0 {
			t.Fatalf("got %v, want empty", got)
		}
	})
}

func TestBuildExpansionSubgoals(t *testing.T) {
	gaps := []gapSpec{
		{Query: "reported", ReportedBy: 1},
		{Query: "whole draft", ReportedBy: -1},
		{Query: "out of range", ReportedBy: 9},
	}
	specs := buildExpansionSubgoals(gaps, 2)
	if len(specs) != 3 {
		t.Fatalf("got %d specs, want 3", len(specs))
	}
	if !reflect.DeepEqual(specs[0].DependsOn, []string{"1"}) {
		t.Fatalf("specs[0].DependsOn = %v, want [1]", specs[0].DependsOn)
	}
	if specs[1].DependsOn != nil {
		t.Fatalf("specs[1].DependsOn = %v, want nil (no reporter)", specs[1].DependsOn)
	}
	if specs[2].DependsOn != nil {
		t.Fatalf("specs[2].DependsOn = %v, want nil (out of range)", specs[2].DependsOn)
	}
}
