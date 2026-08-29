package dragon

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type ScoreStat struct {
	Count          int `json:"count"`
	RetrievalHits  int `json:"retrieval_hits"`
	AnswerContains int `json:"answer_contains_gold"`
}

type ScoreReport struct {
	Total          int                   `json:"total"`
	Matched        int                   `json:"matched"`
	RetrievalHits  int                   `json:"retrieval_hits"`
	AnswerContains int                   `json:"answer_contains_gold"`
	Types          map[string]*ScoreStat `json:"types"`
}

func (r *ScoreReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "total=%d matched=%d retrieval_hit=%d/%d answer_contains=%d/%d",
		r.Total, r.Matched, r.RetrievalHits, r.Matched, r.AnswerContains, r.Matched)
	types := make([]string, 0, len(r.Types))
	for t := range r.Types {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		st := r.Types[t]
		fmt.Fprintf(&b, " %s(n=%d retrieval=%d answer=%d)", t, st.Count, st.RetrievalHits, st.AnswerContains)
	}
	return b.String()
}

func Score(submission map[string]SubmissionEntry, gold []GoldQA) (*ScoreReport, error) {
	rep := &ScoreReport{Total: len(gold), Types: map[string]*ScoreStat{}}
	for _, g := range gold {
		entry, ok := submission[strconv.Itoa(g.PublicID)]
		if !ok {
			continue
		}
		rep.Matched++

		st, ok := rep.Types[g.Type]
		if !ok {
			st = &ScoreStat{}
			rep.Types[g.Type] = st
		}
		st.Count++

		wantIDs, err := flattenTextIDs(g.TextIDs)
		if err != nil {
			return nil, fmt.Errorf("dragon: score public_id %d: %w", g.PublicID, err)
		}
		if retrievalHit(entry.FoundIDs, wantIDs) {
			rep.RetrievalHits++
			st.RetrievalHits++
		}
		if answerContainsGold(entry.ModelAnswer, g.Answer) {
			rep.AnswerContains++
			st.AnswerContains++
		}
	}
	return rep, nil
}

func retrievalHit(foundIDs, wantIDs []string) bool {
	if len(wantIDs) == 0 {
		return false
	}
	found := make(map[string]struct{}, len(foundIDs))
	for _, id := range foundIDs {
		found[id] = struct{}{}
	}
	for _, want := range wantIDs {
		if _, ok := found[want]; ok {
			return true
		}
	}
	return false
}

func answerContainsGold(modelAnswer, goldAnswer string) bool {
	gold := strings.TrimSpace(goldAnswer)
	if gold == "" {
		return false
	}
	return strings.Contains(strings.ToLower(modelAnswer), strings.ToLower(gold))
}

func flattenTextIDs(raw string) ([]string, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("dragon: parse text_ids %q: %w", raw, err)
	}
	seen := map[string]struct{}{}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case float64:
			id := strconv.FormatInt(int64(t), 10)
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		case []any:
			for _, item := range t {
				walk(item)
			}
		}
	}
	walk(v)
	return out, nil
}

func SaveScoreReport(path string, rep *ScoreReport) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("dragon: encode score report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("dragon: write score report: %w", err)
	}
	return nil
}
