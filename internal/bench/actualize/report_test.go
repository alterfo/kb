package actualize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReportScoresBeforeAndAfter(t *testing.T) {
	questions := Questions()
	before := make([]string, len(questions))
	after := make([]string, len(questions))
	for i, q := range questions {
		before[i] = q.AnswerBefore
		after[i] = q.AnswerAfter
	}

	rep, err := BuildReport(before, after)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.Summary.Total != 15 || rep.Summary.Affected != 10 || rep.Summary.Control != 5 {
		t.Fatalf("summary counts = total=%d affected=%d control=%d, want 15/10/5", rep.Summary.Total, rep.Summary.Affected, rep.Summary.Control)
	}
	if rep.Summary.BeforeCorrect != 15 || rep.Summary.AfterCorrect != 15 {
		t.Fatalf("correct counts = before=%d after=%d, want 15/15", rep.Summary.BeforeCorrect, rep.Summary.AfterCorrect)
	}
	if rep.Summary.AffectedUpdated != 10 || rep.Summary.ControlStable != 5 {
		t.Fatalf("updated/stable = affected=%d control=%d, want 10/5", rep.Summary.AffectedUpdated, rep.Summary.ControlStable)
	}

	for i, row := range rep.Questions {
		if !row.BeforeScore || !row.AfterScore {
			t.Errorf("question %d %q: before_score=%v after_score=%v, want true/true", i, row.Question, row.BeforeScore, row.AfterScore)
		}
		if row.BeforeAnswer != questions[i].AnswerBefore || row.AfterAnswer != questions[i].AnswerAfter {
			t.Errorf("question %d answers not preserved: %+v", i, row)
		}
	}
}

func TestBuildReportDetectsWrongAnswers(t *testing.T) {
	questions := Questions()
	before := make([]string, len(questions))
	after := make([]string, len(questions))
	for i := range questions {
		before[i] = "неверный ответ"
		after[i] = "другой неверный ответ"
	}

	rep, err := BuildReport(before, after)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.Summary.BeforeCorrect != 0 || rep.Summary.AfterCorrect != 0 {
		t.Fatalf("correct counts = before=%d after=%d, want 0/0", rep.Summary.BeforeCorrect, rep.Summary.AfterCorrect)
	}
	if rep.Summary.AffectedUpdated != 0 || rep.Summary.ControlStable != 0 {
		t.Fatalf("updated/stable = affected=%d control=%d, want 0/0", rep.Summary.AffectedUpdated, rep.Summary.ControlStable)
	}
}

func TestBuildReportExcludesFalsePositives(t *testing.T) {
	questions := Questions()
	before := make([]string, len(questions))
	after := make([]string, len(questions))
	for i, q := range questions {
		before[i] = q.AnswerBefore
		after[i] = q.AnswerAfter
	}

	roadmapIdx, budgetIdx, officeIdx := -1, -1, -1
	for i, q := range questions {
		switch q.TargetDocID {
		case "roadmap":
			if roadmapIdx == -1 {
				roadmapIdx = i
			}
		case "budget":
			if budgetIdx == -1 {
				budgetIdx = i
			}
		case "office":
			officeIdx = i
		}
	}
	if roadmapIdx == -1 || budgetIdx == -1 || officeIdx == -1 {
		t.Fatalf("fixture questions missing roadmap/budget/office targets")
	}

	// Affected question whose before-answer already contains the after-gold: not a
	// genuine transition, must not count as updated.
	before[roadmapIdx] = questions[roadmapIdx].AnswerAfter

	// Control question whose after-answer leaks an unrelated affected correction:
	// the office fact itself didn't change, but the metric must not call it stable.
	after[officeIdx] = questions[officeIdx].AnswerBefore + " (бюджет вырос до " + questions[budgetIdx].AnswerAfter + ")"

	rep, err := BuildReport(before, after)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.Summary.AffectedUpdated != 9 {
		t.Errorf("AffectedUpdated = %d, want 9 (roadmap excluded: before already contained the after-gold)", rep.Summary.AffectedUpdated)
	}
	if rep.Summary.ControlStable != 4 {
		t.Errorf("ControlStable = %d, want 4 (office excluded: after leaks an unrelated affected correction)", rep.Summary.ControlStable)
	}
}

func TestBuildReportRejectsAnswerCountMismatch(t *testing.T) {
	if _, err := BuildReport([]string{"one"}, []string{"two"}); err == nil {
		t.Fatal("BuildReport with mismatched answer counts returned nil error")
	}
}

func TestSaveReportCreatesParentAndWritesJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "run.json")
	rep, err := BuildReport([]string{"15 марта 2026"}, nil)
	if err == nil {
		t.Fatal("BuildReport with mismatched answer counts should fail before SaveReport")
	}

	questions := Questions()
	before := make([]string, len(questions))
	after := make([]string, len(questions))
	for i, q := range questions {
		before[i] = q.AnswerBefore
		after[i] = q.AnswerAfter
	}
	rep, err = BuildReport(before, after)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}

	if err := SaveReport(path, rep); err != nil {
		t.Fatalf("SaveReport: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var got Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if got.Summary.Total != rep.Summary.Total {
		t.Fatalf("round-trip summary total = %d, want %d", got.Summary.Total, rep.Summary.Total)
	}
}
