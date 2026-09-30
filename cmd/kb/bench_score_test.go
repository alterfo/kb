package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
	runbench "github.com/alterfo/kb/internal/bench/run"
	"github.com/alterfo/kb/internal/config"
)

func writeScoreSubmission(t *testing.T, path string, answers []runbench.Answer) string {
	t.Helper()
	lines := make([]string, 0, len(answers))
	for _, a := range answers {
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBenchScoreWritesReportAndHistory(t *testing.T) {
	dir := t.TempDir()
	questionsPath := filepath.Join(dir, "questions.jsonl")
	if err := corpus.WriteQuestions(questionsPath, []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", GoldAnswer: "alpha beta", AnswerFacts: []string{"alpha", "beta"}, ExpectedDocIDs: []string{"dsid_d1"}},
		{ID: "q2", Type: "basic", Text: "two?", GoldAnswer: "gamma", ExpectedDocIDs: []string{"dsid_d2"}},
	}); err != nil {
		t.Fatalf("write questions: %v", err)
	}
	submissionPath := writeScoreSubmission(t, filepath.Join(dir, "answers.jsonl"), []runbench.Answer{
		{QuestionID: "q1", Answer: "alpha beta", DocumentIDs: []string{"dsid_d1"}},
		{QuestionID: "q2", Answer: "different", DocumentIDs: []string{"dsid_x"}},
	})
	out := filepath.Join(dir, "answers.score.json")
	history := filepath.Join(dir, "answers.score.history.json")

	var stdout, stderr bytes.Buffer
	code := runBenchScoreCmd([]string{"-questions", questionsPath, "-out", out, "-history", history, submissionPath}, config.Env{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read score report: %v", err)
	}
	var rep runbench.ScoreReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("unmarshal score report: %v", err)
	}
	if rep.Total != 2 || rep.Matched != 2 {
		t.Errorf("Total/Matched = %d/%d, want 2/2", rep.Total, rep.Matched)
	}
	if rep.RetrievalHits != 1 {
		t.Errorf("RetrievalHits = %d, want 1", rep.RetrievalHits)
	}
	if rep.AnswerContains != 1 {
		t.Errorf("AnswerContains = %d, want 1", rep.AnswerContains)
	}
	if rep.AvgFactsCoverage != 1 {
		t.Errorf("AvgFactsCoverage = %v, want 1", rep.AvgFactsCoverage)
	}
	basic := rep.Types["basic"]
	if basic == nil || basic.Count != 2 || basic.RetrievalHits != 1 || basic.AnswerContains != 1 || basic.FactsCoverage != 1 {
		t.Errorf("Types[basic] = %+v", basic)
	}

	histData, err := os.ReadFile(history)
	if err != nil {
		t.Fatalf("read score history: %v", err)
	}
	var hist []runbench.ScoreHistoryEntry
	if err := json.Unmarshal(histData, &hist); err != nil {
		t.Fatalf("unmarshal score history: %v", err)
	}
	if len(hist) != 1 || hist[0].Report.Total != 2 {
		t.Errorf("history = %+v, want one entry with total 2", hist)
	}
}

func TestBenchScoreDefaultOutPath(t *testing.T) {
	dir := t.TempDir()
	questionsPath := filepath.Join(dir, "questions.jsonl")
	if err := corpus.WriteQuestions(questionsPath, []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", GoldAnswer: "alpha"},
	}); err != nil {
		t.Fatalf("write questions: %v", err)
	}
	submissionPath := writeScoreSubmission(t, filepath.Join(dir, "answers.jsonl"), []runbench.Answer{
		{QuestionID: "q1", Answer: "alpha"},
	})

	var stdout, stderr bytes.Buffer
	code := runBenchScoreCmd([]string{"-questions", questionsPath, submissionPath}, config.Env{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(submissionPath + ".score.json"); err != nil {
		t.Errorf("expected default score report: %v", err)
	}
	if _, err := os.Stat(submissionPath + ".score.json.history.json"); err != nil {
		t.Errorf("expected default score history: %v", err)
	}
}

func TestBenchScoreMissingArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBenchScoreCmd(nil, config.Env{}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "expected one argument") {
		t.Errorf("stderr = %q, want usage error", stderr.String())
	}
}

func TestBenchScoreMissingQuestionsFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBenchScoreCmd([]string{filepath.Join(t.TempDir(), "answers.jsonl")}, config.Env{}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-questions is required") {
		t.Errorf("stderr = %q, want -questions requirement", stderr.String())
	}
}

func TestBenchScoreUnreadableSubmission(t *testing.T) {
	dir := t.TempDir()
	questionsPath := filepath.Join(dir, "questions.jsonl")
	if err := corpus.WriteQuestions(questionsPath, []corpus.Question{{ID: "q1", Type: "basic", Text: "one?"}}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runBenchScoreCmd([]string{"-questions", questionsPath, filepath.Join(dir, "missing.jsonl")}, config.Env{}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "open submission") {
		t.Errorf("stderr = %q, want open submission error", stderr.String())
	}
}

func TestBenchScoreJudgeFlagNoContextMakesNoLLMCalls(t *testing.T) {
	dir := t.TempDir()
	questionsPath := filepath.Join(dir, "questions.jsonl")
	if err := corpus.WriteQuestions(questionsPath, []corpus.Question{
		{ID: "q1", Type: "basic", Text: "one?", GoldAnswer: "alpha", ExpectedDocIDs: []string{"dsid_d1"}},
	}); err != nil {
		t.Fatalf("write questions: %v", err)
	}
	submissionPath := writeScoreSubmission(t, filepath.Join(dir, "answers.jsonl"), []runbench.Answer{
		{QuestionID: "q1", Answer: "alpha", DocumentIDs: []string{"dsid_d1"}},
	})

	var stdout, stderr bytes.Buffer
	code := runBenchScoreCmd([]string{"-questions", questionsPath, "-judge-faithfulness", submissionPath}, config.Env{LLMModel: "test-model"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	var rep runbench.ScoreReport
	data, err := os.ReadFile(submissionPath + ".score.json")
	if err != nil {
		t.Fatalf("read score report: %v", err)
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("unmarshal score report: %v", err)
	}
	if rep.AvgFaithfulness != 0 {
		t.Errorf("AvgFaithfulness = %v, want 0 (no context chunks to judge)", rep.AvgFaithfulness)
	}
}
