package dynamic

import (
	"encoding/json"
	"fmt"
	"os"
)

type actualizationQuestion struct {
	Question       string `json:"question"`
	BeforeAnswer   string `json:"before_answer"`
	AfterAnswer    string `json:"after_answer"`
	ExpectedBefore string `json:"expected_before"`
	ExpectedAfter  string `json:"expected_after"`
	Affected       bool   `json:"affected"`
	TargetDocID    string `json:"target_doc_id"`
}

type actualizationRun struct {
	Questions []actualizationQuestion `json:"questions"`
}

func LoadActualization(path string) ([]Case, map[string]string, map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dynamic: read actualization run: %w", err)
	}
	var run actualizationRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, nil, nil, fmt.Errorf("dynamic: decode actualization run: %w", err)
	}
	return FromActualization(run.Questions)
}

func FromActualization(qs []actualizationQuestion) ([]Case, map[string]string, map[string]string, error) {
	cases := make([]Case, 0, len(qs))
	before := make(map[string]string, len(qs))
	after := make(map[string]string, len(qs))
	for i, q := range qs {
		id := fmt.Sprintf("q%03d", i+1)
		c := Case{ID: id, Question: q.Question, Language: "ru", Current: []string{q.ExpectedAfter}}
		if q.Affected {
			c.Stale = []string{q.ExpectedBefore}
		} else {
			c.Control = true
			for _, o := range qs {
				if o.Affected && o.TargetDocID != q.TargetDocID {
					c.Stale = append(c.Stale, o.ExpectedAfter)
				}
			}
		}
		cases = append(cases, c)
		before[id] = q.BeforeAnswer
		after[id] = q.AfterAnswer
	}
	return cases, before, after, nil
}
