package legaleval

import (
	"strings"
	"testing"
)

func TestParseQAPairs(t *testing.T) {
	src := `[
	  {"question": "Q1", "expected_articles": ["a/ст1"], "expected_plenum_points": ["p/п1"], "justification": "j1"},
	  {"question": "Q2", "expected_articles": ["a/ст2"]}
	]`
	pairs, err := ParseQAPairs(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseQAPairs: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("got %d pairs, want 2", len(pairs))
	}
	if pairs[0].Question != "Q1" || len(pairs[0].ExpectedArticles) != 1 || pairs[0].ExpectedArticles[0] != "a/ст1" {
		t.Fatalf("pair 0 = %+v", pairs[0])
	}
	if pairs[0].ExpectedPlenumPoints[0] != "p/п1" || pairs[0].Justification != "j1" {
		t.Fatalf("pair 0 plenum/justification = %+v", pairs[0])
	}
	if len(pairs[1].ExpectedPlenumPoints) != 0 {
		t.Fatalf("pair 1 plenum points = %v, want none", pairs[1].ExpectedPlenumPoints)
	}
}

func TestParseQAPairsInvalidJSON(t *testing.T) {
	if _, err := ParseQAPairs(strings.NewReader("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseQAPairsEmptyQuestion(t *testing.T) {
	src := `[{"question": "  "}]`
	if _, err := ParseQAPairs(strings.NewReader(src)); err == nil {
		t.Fatal("expected error for empty question")
	}
}

func TestLoadQAPairsMissingFile(t *testing.T) {
	if _, err := LoadQAPairs("no-such-file.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadQAPairsGoldFixture(t *testing.T) {
	pairs, err := LoadQAPairs("../../../internal/importer/legalru/testdata/gold/qa_pairs.json")
	if err != nil {
		t.Fatalf("LoadQAPairs gold: %v", err)
	}
	if len(pairs) != 8 {
		t.Fatalf("gold qa_pairs.json has %d pairs, want 8", len(pairs))
	}
	for _, p := range pairs {
		if len(p.ExpectedArticles) == 0 {
			t.Fatalf("pair %q has no expected articles", p.Question)
		}
		if p.Justification == "" {
			t.Fatalf("pair %q has no justification", p.Question)
		}
	}
}
