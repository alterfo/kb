package run

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/engine/report"
)

func sampleQuestions(t *testing.T) []corpus.Question {
	t.Helper()
	qs, warns, err := corpus.LoadQuestions(filepath.Join("..", "corpus", "testdata", "questions-sample.jsonl"))
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	return qs
}

func TestRunnerWritesSubmissionAndReport(t *testing.T) {
	out := filepath.Join(t.TempDir(), "answers.jsonl")
	ask := func(ctx context.Context, q corpus.Question) (string, []string) {
		if q.Type == "info_not_found" {
			return report.NotFoundSentinel, nil
		}
		return "The limit is 10 MiB per file.", []string{"dsid_ae068ee4aa9640159427cd941bef0238"}
	}

	r := &Runner{Questions: sampleQuestions(t), OutPath: out, Ask: ask}
	rep, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read answers: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("answers lines = %d, want 3", len(lines))
	}
	var first Answer
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("unmarshal answer line: %v", err)
	}
	if first.QuestionID != "qst_0001" || first.Answer == "" || len(first.DocumentIDs) != 1 {
		t.Fatalf("first line = %+v", first)
	}

	if rep.Total != 3 {
		t.Errorf("Total = %d, want 3", rep.Total)
	}
	if rep.Types["info_not_found"].Abstain != 1 {
		t.Errorf("info_not_found abstain = %d, want 1", rep.Types["info_not_found"].Abstain)
	}
	if rep.Types["basic"].AvgRecall <= 0 && rep.Types["basic"].Count > 0 {
		t.Errorf("basic recall should be > 0 when the gold doc was returned")
	}
	if !strings.Contains(rep.Summary(), "total=3") {
		t.Errorf("Summary missing totals: %q", rep.Summary())
	}
}

func TestRunnerPreservesQuestionOrderWithConcurrency(t *testing.T) {
	out := filepath.Join(t.TempDir(), "answers.jsonl")
	r := &Runner{Questions: sampleQuestions(t), OutPath: out, Concurrency: 3,
		Ask: func(ctx context.Context, q corpus.Question) (string, []string) {
			return "answer for " + q.ID, nil
		}}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	wantOrder := []string{"qst_0001", "qst_0471", "qst_0481"}
	for i, want := range wantOrder {
		var a Answer
		if err := json.Unmarshal([]byte(lines[i]), &a); err != nil {
			t.Fatal(err)
		}
		if a.QuestionID != want {
			t.Fatalf("line %d = %s, want %s", i, a.QuestionID, want)
		}
	}
}

func TestBuildReportLanguageAggregation(t *testing.T) {
	qs := []corpus.Question{
		{ID: "q1", Type: "basic", Language: "ru", ExpectedDocIDs: []string{"a"}},
		{ID: "q2", Type: "basic", Language: "ru", ExpectedDocIDs: []string{"b", "c"}},
		{ID: "q3", Type: "basic", Language: "en", ExpectedDocIDs: []string{"x"}},
		{ID: "q4", Type: "basic"},
		{ID: "q5", Type: "high_level", Language: "en"},
	}
	answers := []Answer{
		{QuestionID: "q1", Answer: "a (citation)", DocumentIDs: []string{"a"}},
		{QuestionID: "q2", Answer: "partial", DocumentIDs: []string{"b"}},
		{QuestionID: "q3", Answer: "missing", DocumentIDs: nil},
		{QuestionID: "q4", Answer: report.NotFoundSentinel},
		{QuestionID: "q5", Answer: "answer (citation)"},
	}

	rep := buildReport(qs, answers)

	ru := rep.Languages["ru"]
	if ru == nil {
		t.Fatal("Languages[ru] missing")
	}
	if ru.Count != 2 || ru.Abstain != 0 || ru.Cited != 1 {
		t.Errorf("ru stat = %+v, want count=2 abstain=0 cited=1", ru)
	}
	if math.Abs(ru.AvgRecall-0.75) > 1e-9 {
		t.Errorf("ru AvgRecall = %v, want 0.75", ru.AvgRecall)
	}

	en := rep.Languages["en"]
	if en == nil {
		t.Fatal("Languages[en] missing")
	}
	if en.Count != 2 || en.Abstain != 0 || en.Cited != 1 {
		t.Errorf("en stat = %+v, want count=2 abstain=0 cited=1", en)
	}
	if en.AvgRecall != 0 {
		t.Errorf("en AvgRecall = %v, want 0", en.AvgRecall)
	}

	unknown := rep.Languages["unknown"]
	if unknown == nil {
		t.Fatal("Languages[unknown] missing")
	}
	if unknown.Count != 1 || unknown.Abstain != 1 || unknown.Cited != 0 {
		t.Errorf("unknown stat = %+v, want count=1 abstain=1 cited=0", unknown)
	}

	basic := rep.Types["basic"]
	if basic == nil {
		t.Fatal("Types[basic] missing")
	}
	if basic.Count != 4 {
		t.Errorf("basic Count = %d, want 4", basic.Count)
	}

	summary := rep.Summary()
	for _, want := range []string{
		"ru(n=2 abstain=0 cited=1 recall=0.75)",
		"en(n=2 abstain=0 cited=1)",
		"unknown(n=1 abstain=1 cited=0)",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("Summary() = %q, want it to contain %q", summary, want)
		}
	}
}

func TestFilterQuestions(t *testing.T) {
	qs := sampleQuestions(t)

	only := FilterQuestions(qs, map[string]struct{}{"basic": {}, "high_level": {}}, 0)
	if len(only) != 2 {
		t.Fatalf("type filter kept %d, want 2", len(only))
	}

	limited := FilterQuestions(qs, nil, 2)
	if len(limited) != 2 {
		t.Fatalf("limit kept %d, want 2", len(limited))
	}

	none := FilterQuestions(qs, map[string]struct{}{"constrained": {}}, 0)
	if len(none) != 0 {
		t.Fatalf("impossible type filter kept %d, want 0", len(none))
	}
}

func TestIsAbstain(t *testing.T) {
	if !isAbstain(report.NotFoundSentinel) {
		t.Error("sentinel must count as abstain")
	}
	if !isAbstain(abstainVerdictText) {
		t.Error("got abstention verdict must count as abstain")
	}
	if isAbstain("The limit is 10 MiB.") {
		t.Error("regular answer must not count as abstain")
	}
}

func TestCorpusDocumentIDsFiltersSyntheticAndDuplicates(t *testing.T) {
	in := []string{
		"",
		"doc-real-1",
		"set-summary",
		"global:summary",
		"community:abc",
		"doc-real-1",
		"doc-real-2",
		"",
	}
	got := CorpusDocumentIDs(in)
	want := []string{"doc-real-1", "doc-real-2"}
	if len(got) != len(want) {
		t.Fatalf("CorpusDocumentIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CorpusDocumentIDs[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestIsSyntheticBenchDocID(t *testing.T) {
	real := []string{"doc-real-1", "leon-repo-main", "dsid_111"}
	for _, id := range real {
		if isSyntheticBenchDocID(id) {
			t.Errorf("isSyntheticBenchDocID(%q) = true, want false", id)
		}
	}
	synthetic := []string{"set-summary", "global:summary", "community:abc"}
	for _, id := range synthetic {
		if !isSyntheticBenchDocID(id) {
			t.Errorf("isSyntheticBenchDocID(%q) = false, want true", id)
		}
	}
}
