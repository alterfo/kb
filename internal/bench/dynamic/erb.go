package dynamic

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/bench/corpus"
)

type staleSpec struct {
	Current []string `json:"current"`
	Stale   []string `json:"stale"`
}

func LoadERB(questionsPath, stalePath string) ([]Case, error) {
	data, err := os.ReadFile(stalePath)
	if err != nil {
		return nil, fmt.Errorf("dynamic: read stale spec: %w", err)
	}
	var specs map[string]staleSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		return nil, fmt.Errorf("dynamic: decode stale spec: %w", err)
	}
	qs, _, err := corpus.LoadQuestions(questionsPath)
	if err != nil {
		return nil, err
	}
	var cases []Case
	matched := make(map[string]struct{}, len(specs))
	for _, q := range qs {
		spec, ok := specs[q.ID]
		if !ok {
			continue
		}
		matched[q.ID] = struct{}{}
		lang := q.Language
		if lang == "" {
			lang = "en"
		}
		cases = append(cases, Case{ID: q.ID, Question: q.Text, Language: lang, Current: spec.Current, Stale: spec.Stale})
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("dynamic: no questions in %s match the stale spec %s", questionsPath, stalePath)
	}
	var missing []string
	for id := range specs {
		if _, ok := matched[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("dynamic: stale spec %s names questions absent from %s: %s", stalePath, questionsPath, strings.Join(missing, ", "))
	}
	return cases, nil
}
