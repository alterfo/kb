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

func TestBenchEvolveMissingFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBenchEvolveCmd(nil, config.Env{}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-corpus and -questions are required") {
		t.Errorf("stderr = %q, want corpus/questions requirement", stderr.String())
	}
}

func TestBenchEvolveScoreAndSave(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "stage0-native.json")
	submission := `{"question_id":"q1","answer":"alpha beta gamma","document_ids":["dsid_d1"]}
{"question_id":"q2","answer":"completely different","document_ids":["dsid_x"]}
`
	if err := os.WriteFile(outPath, []byte(submission), 0o644); err != nil {
		t.Fatal(err)
	}

	questions := []corpus.Question{
		{ID: "q1", Type: "single-doc", ExpectedDocIDs: []string{"dsid_d1"}, GoldAnswer: "alpha beta", Language: "en"},
		{ID: "q2", Type: "single-doc", ExpectedDocIDs: []string{"dsid_y"}, GoldAnswer: "expected", Language: "en"},
	}

	var stdout bytes.Buffer
	if err := benchEvolveScoreAndSave(outPath, questions, &stdout); err != nil {
		t.Fatalf("benchEvolveScoreAndSave: %v", err)
	}
	if !strings.Contains(stdout.String(), "total=2 matched=2") {
		t.Errorf("stdout = %q, want score summary", stdout.String())
	}

	scorePath := outPath + ".score.json"
	if _, err := os.Stat(scorePath); err != nil {
		t.Fatalf("score report not written: %v", err)
	}
	rep, err := loadScoreReportForTest(scorePath)
	if err != nil {
		t.Fatalf("load score report: %v", err)
	}
	if rep.Total != 2 || rep.Matched != 2 {
		t.Errorf("score report = total %d matched %d, want 2/2", rep.Total, rep.Matched)
	}
	if rep.RetrievalHits != 1 {
		t.Errorf("RetrievalHits = %d, want 1", rep.RetrievalHits)
	}

	historyPath := scorePath + ".history.json"
	if _, err := os.Stat(historyPath); err != nil {
		t.Fatalf("score history not written: %v", err)
	}
	hist, err := runbench.LoadScoreHistory(historyPath)
	if err != nil {
		t.Fatalf("load score history: %v", err)
	}
	if len(hist) != 1 || hist[0].Report.Matched != 2 {
		t.Errorf("score history = %d entries, want 1 matched=2", len(hist))
	}
}

func loadScoreReportForTest(path string) (*runbench.ScoreReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep runbench.ScoreReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, err
	}
	return &rep, nil
}
