package run

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/kljensen/snowball/english"
)

type ScoreStat struct {
	Count          int     `json:"count"`
	RetrievalHits  int     `json:"retrieval_hits"`
	AnswerContains int     `json:"answer_contains_gold"`
	FactsCoverage  float64 `json:"avg_facts_coverage,omitempty"`
}

type ScoreReport struct {
	Total            int                   `json:"total"`
	Matched          int                   `json:"matched"`
	RetrievalHits    int                   `json:"retrieval_hits"`
	AnswerContains   int                   `json:"answer_contains_gold"`
	AvgFactsCoverage float64               `json:"avg_facts_coverage,omitempty"`
	Types            map[string]*ScoreStat `json:"types"`
}

func (r *ScoreReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "total=%d matched=%d retrieval_hit=%d/%d answer_contains=%d/%d",
		r.Total, r.Matched, r.RetrievalHits, r.Matched, r.AnswerContains, r.Matched)
	if r.AvgFactsCoverage > 0 {
		fmt.Fprintf(&b, " facts_coverage=%.2f", r.AvgFactsCoverage)
	}
	types := make([]string, 0, len(r.Types))
	for t := range r.Types {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		st := r.Types[t]
		extra := ""
		if st.Count > 0 && st.FactsCoverage > 0 {
			extra = fmt.Sprintf(" facts=%.2f", st.FactsCoverage)
		}
		fmt.Fprintf(&b, " %s(n=%d retrieval=%d answer=%d%s)", t, st.Count, st.RetrievalHits, st.AnswerContains, extra)
	}
	return b.String()
}

func Score(submission map[string]Answer, gold []corpus.Question) (*ScoreReport, error) {
	rep := &ScoreReport{Total: len(gold), Types: map[string]*ScoreStat{}}
	factsSums := map[string]float64{}
	factsCounts := map[string]int{}
	for _, q := range gold {
		entry, ok := submission[q.ID]
		if !ok {
			continue
		}
		rep.Matched++

		st, ok := rep.Types[q.Type]
		if !ok {
			st = &ScoreStat{}
			rep.Types[q.Type] = st
		}
		st.Count++

		if retrievalHit(entry.DocumentIDs, q.ExpectedDocIDs) {
			rep.RetrievalHits++
			st.RetrievalHits++
		}
		if answerContainsGold(entry.Answer, q.GoldAnswer) {
			rep.AnswerContains++
			st.AnswerContains++
		}
		if len(q.AnswerFacts) > 0 {
			covered := 0
			for _, fact := range q.AnswerFacts {
				if answerContainsGold(entry.Answer, fact) {
					covered++
				}
			}
			frac := float64(covered) / float64(len(q.AnswerFacts))
			factsSums[q.Type] += frac
			factsCounts[q.Type]++
		}
	}
	var totalFactsSum float64
	var totalFactsCount int
	for t, sum := range factsSums {
		rep.Types[t].FactsCoverage = sum / float64(factsCounts[t])
		totalFactsSum += sum
		totalFactsCount += factsCounts[t]
	}
	if totalFactsCount > 0 {
		rep.AvgFactsCoverage = totalFactsSum / float64(totalFactsCount)
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

var setItemRe = regexp.MustCompile(`'([^']*)'`)
var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

func stemSequence(s string) []string {
	tokens := wordRe.FindAllString(strings.ToLower(s), -1)
	stems := make([]string, len(tokens))
	for i, t := range tokens {
		stems[i] = english.Stem(t, true)
	}
	return stems
}

func phraseStemsPresent(modelStems []string, phrase string) bool {
	phraseStems := stemSequence(phrase)
	if len(phraseStems) == 0 || len(phraseStems) > len(modelStems) {
		return false
	}
	for i := 0; i+len(phraseStems) <= len(modelStems); i++ {
		match := true
		for j, ps := range phraseStems {
			if modelStems[i+j] != ps {
				match = false
				break
			}
		}
		if match {
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
	modelStems := stemSequence(modelAnswer)
	if strings.HasPrefix(gold, "[") && strings.HasSuffix(gold, "]") {
		items := setItemRe.FindAllStringSubmatch(gold, -1)
		if len(items) == 0 {
			return false
		}
		for _, m := range items {
			item := strings.TrimSpace(m[1])
			if item == "" {
				continue
			}
			if !phraseStemsPresent(modelStems, item) {
				return false
			}
		}
		return true
	}
	return phraseStemsPresent(modelStems, gold)
}

func LoadSubmission(path string) (map[string]Answer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("bench: open submission: %w", err)
	}
	defer f.Close()

	out := map[string]Answer{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\uFEFF"))
		if line == "" {
			continue
		}
		var a Answer
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			return nil, fmt.Errorf("bench: parse submission line %d: %w", lineNo, err)
		}
		if a.QuestionID == "" {
			continue
		}
		out[a.QuestionID] = a
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("bench: scan submission: %w", err)
	}
	return out, nil
}

func SaveScoreReport(path string, rep *ScoreReport) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: encode score report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("bench: write score report: %w", err)
	}
	return nil
}

type ScoreHistoryEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Report    ScoreReport `json:"report"`
}

func AppendScoreHistory(path string, rep *ScoreReport) error {
	return appendScoreHistoryAt(path, rep, time.Now().UTC())
}

func appendScoreHistoryAt(path string, rep *ScoreReport, timestamp time.Time) error {
	entries, err := LoadScoreHistory(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	entries = append(entries, ScoreHistoryEntry{Timestamp: timestamp, Report: *rep})
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: encode score history: %w", err)
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("bench: create score history dir: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("bench: write score history: %w", err)
	}
	return nil
}

func LoadScoreHistory(path string) ([]ScoreHistoryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []ScoreHistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("bench: parse score history: %w", err)
	}
	return entries, nil
}
