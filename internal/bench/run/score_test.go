package run

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
)

func TestScoreRetrievalHitAndAnswerContains(t *testing.T) {
	submission := map[string]Answer{
		"q1": {QuestionID: "q1", Answer: "The answer is alpha beta gamma.", DocumentIDs: []string{"dsid_d1", "dsid_d2"}},
		"q2": {QuestionID: "q2", Answer: "Completely different answer.", DocumentIDs: []string{"dsid_x"}},
	}
	gold := []corpus.Question{
		{ID: "q1", Type: "basic", ExpectedDocIDs: []string{"dsid_d1"}, GoldAnswer: "alpha beta"},
		{ID: "q2", Type: "basic", ExpectedDocIDs: []string{"dsid_y"}, GoldAnswer: "expected"},
	}

	rep, err := Score(submission, gold)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if rep.Total != 2 || rep.Matched != 2 {
		t.Fatalf("Total/Matched = %d/%d, want 2/2", rep.Total, rep.Matched)
	}
	if rep.RetrievalHits != 1 {
		t.Fatalf("RetrievalHits = %d, want 1", rep.RetrievalHits)
	}
	if rep.AnswerContains != 1 {
		t.Fatalf("AnswerContains = %d, want 1", rep.AnswerContains)
	}
	st := rep.Types["basic"]
	if st == nil || st.Count != 2 || st.RetrievalHits != 1 || st.AnswerContains != 1 {
		t.Fatalf("Types[basic] = %+v", st)
	}
}

func TestScoreUnmatchedGoldSkipped(t *testing.T) {
	submission := map[string]Answer{}
	gold := []corpus.Question{{ID: "q999", Type: "basic", GoldAnswer: "x"}}

	rep, err := Score(submission, gold)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if rep.Total != 1 || rep.Matched != 0 {
		t.Fatalf("Total/Matched = %d/%d, want 1/0", rep.Total, rep.Matched)
	}
}

func TestScoreFactsCoveragePartial(t *testing.T) {
	submission := map[string]Answer{
		"q1": {QuestionID: "q1", Answer: "Alpha beta gamma delta.", DocumentIDs: []string{"dsid_d1"}},
	}
	gold := []corpus.Question{
		{
			ID:             "q1",
			Type:           "basic",
			ExpectedDocIDs: []string{"dsid_d1"},
			GoldAnswer:     "alpha beta",
			AnswerFacts:    []string{"alpha beta", "gamma", "omega"},
		},
	}

	rep, err := Score(submission, gold)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if rep.AnswerContains != 1 {
		t.Fatalf("AnswerContains = %d, want 1", rep.AnswerContains)
	}
	st := rep.Types["basic"]
	if st == nil {
		t.Fatal("Types[basic] missing")
	}
	if math.Abs(st.FactsCoverage-2.0/3.0) > 1e-9 {
		t.Fatalf("FactsCoverage = %v, want %.9f", st.FactsCoverage, 2.0/3.0)
	}
	if math.Abs(rep.AvgFactsCoverage-2.0/3.0) > 1e-9 {
		t.Fatalf("AvgFactsCoverage = %v, want %.9f", rep.AvgFactsCoverage, 2.0/3.0)
	}
}

func TestScoreEmptyGoldAnswerNeverCounted(t *testing.T) {
	submission := map[string]Answer{
		"q1": {QuestionID: "q1", Answer: "anything"},
	}
	gold := []corpus.Question{{ID: "q1", Type: "basic", GoldAnswer: "  "}}

	rep, err := Score(submission, gold)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if rep.AnswerContains != 0 {
		t.Fatalf("AnswerContains = %d, want 0", rep.AnswerContains)
	}
}

func TestAnswerContainsGoldCaseInsensitive(t *testing.T) {
	if !answerContainsGold("Alpha Beta Gamma", "alpha beta") {
		t.Fatal("expected case-insensitive phrase match")
	}
}

func TestAnswerContainsGoldStemming(t *testing.T) {
	if !answerContainsGold("The cat is running quickly.", "run") {
		t.Fatal("expected stemmed running to match gold run")
	}
}

func TestAnswerContainsGoldRejectsScatteredWords(t *testing.T) {
	if answerContainsGold("Alpha is first. Beta is second.", "alpha beta") {
		t.Fatal("expected no match for scattered words")
	}
}

func TestAnswerContainsGoldSetTypeAllItemsPresent(t *testing.T) {
	if !answerContainsGold("Alpha beta gamma delta", "['alpha', 'gamma']") {
		t.Fatal("expected all set items to match")
	}
}

func TestAnswerContainsGoldSetTypeMissingItem(t *testing.T) {
	if answerContainsGold("Alpha beta", "['alpha', 'gamma']") {
		t.Fatal("expected match to fail when an item is missing")
	}
}

func TestAnswerContainsGoldSetTypeEmptyList(t *testing.T) {
	if answerContainsGold("anything", "[]") {
		t.Fatal("expected empty set list to never match")
	}
}

func TestLoadSubmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "submission.jsonl")
	if err := writeAnswers(path, []Answer{
		{QuestionID: "q1", Answer: "one", DocumentIDs: []string{"d1"}},
		{QuestionID: "q2", Answer: "two"},
	}); err != nil {
		t.Fatalf("writeAnswers: %v", err)
	}

	submission, err := LoadSubmission(path)
	if err != nil {
		t.Fatalf("LoadSubmission: %v", err)
	}
	if len(submission) != 2 {
		t.Fatalf("submission length = %d, want 2", len(submission))
	}
	if submission["q1"].Answer != "one" || len(submission["q1"].DocumentIDs) != 1 {
		t.Fatalf("submission[q1] = %+v", submission["q1"])
	}
}
