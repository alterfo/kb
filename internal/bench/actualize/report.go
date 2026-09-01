package actualize

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alterfo/kb/internal/bench/dragon"
)

type QuestionResult struct {
	Question       string `json:"question"`
	BeforeAnswer   string `json:"before_answer"`
	AfterAnswer    string `json:"after_answer"`
	ExpectedBefore string `json:"expected_before"`
	ExpectedAfter  string `json:"expected_after"`
	Affected       bool   `json:"affected"`
	TargetDocID    string `json:"target_doc_id"`
	BeforeScore    bool   `json:"before_score"`
	AfterScore     bool   `json:"after_score"`
}

type Summary struct {
	Total           int `json:"total"`
	Affected        int `json:"affected"`
	Control         int `json:"control"`
	BeforeCorrect   int `json:"before_correct"`
	AfterCorrect    int `json:"after_correct"`
	AffectedUpdated int `json:"affected_updated"`
	ControlStable   int `json:"control_stable"`
}

type Report struct {
	Summary   Summary          `json:"summary"`
	Questions []QuestionResult `json:"questions"`
}

func BuildReport(before, after []string) (Report, error) {
	questions := Questions()
	if len(before) != len(questions) || len(after) != len(questions) {
		return Report{}, fmt.Errorf("actualize: answer count mismatch: before=%d after=%d questions=%d", len(before), len(after), len(questions))
	}

	rep := Report{Questions: make([]QuestionResult, 0, len(questions))}
	for i, q := range questions {
		beforeScore := dragon.AnswerContainsGold(before[i], q.AnswerBefore)
		afterScore := dragon.AnswerContainsGold(after[i], q.AnswerAfter)
		rep.Questions = append(rep.Questions, QuestionResult{
			Question:       q.Question,
			BeforeAnswer:   before[i],
			AfterAnswer:    after[i],
			ExpectedBefore: q.AnswerBefore,
			ExpectedAfter:  q.AnswerAfter,
			Affected:       q.Affected,
			TargetDocID:    q.TargetDocID,
			BeforeScore:    beforeScore,
			AfterScore:     afterScore,
		})

		if q.Affected {
			rep.Summary.Affected++
		} else {
			rep.Summary.Control++
		}
		if beforeScore {
			rep.Summary.BeforeCorrect++
		}
		if afterScore {
			rep.Summary.AfterCorrect++
		}
		if q.Affected && afterScore &&
			!dragon.AnswerContainsGold(before[i], q.AnswerAfter) &&
			!dragon.AnswerContainsGold(after[i], q.AnswerBefore) {
			rep.Summary.AffectedUpdated++
		}
		if !q.Affected && beforeScore && afterScore &&
			!containsAnyAffectedCorrection(after[i], questions, q.TargetDocID) {
			rep.Summary.ControlStable++
		}
	}
	rep.Summary.Total = len(questions)
	return rep, nil
}

func containsAnyAffectedCorrection(text string, questions []QA, skipDocID string) bool {
	for _, q := range questions {
		if !q.Affected || q.TargetDocID == skipDocID {
			continue
		}
		if dragon.AnswerContainsGold(text, q.AnswerAfter) {
			return true
		}
	}
	return false
}

func SaveReport(path string, rep Report) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("actualize: create report dir: %w", err)
		}
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("actualize: encode report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("actualize: write report: %w", err)
	}
	return nil
}
