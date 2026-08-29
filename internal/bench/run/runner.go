package run

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/engine/report"
)

// Answer is one line of the EnterpriseRAG-Bench submission format.
type Answer struct {
	QuestionID  string   `json:"question_id"`
	Answer      string   `json:"answer"`
	DocumentIDs []string `json:"document_ids"`
}

// AskFunc answers a single benchmark question and reports the document ids
// its pipeline used as evidence.
type AskFunc func(ctx context.Context, q corpus.Question) (string, []string)

// Runner drives the question set through Ask, writes the submission JSONL
// and computes local proxy metrics per question type.
type Runner struct {
	Questions   []corpus.Question
	OutPath     string
	Ask         AskFunc
	Concurrency int
}

type TypeStat struct {
	Count     int     `json:"count"`
	AvgRecall float64 `json:"avg_recall,omitempty"`
	Abstain   int     `json:"abstain"`
	Cited     int     `json:"cited"`
}

type Report struct {
	Total        int                  `json:"total"`
	AbstainTotal int                  `json:"abstain_total"`
	Types        map[string]*TypeStat `json:"types"`
	Languages    map[string]*TypeStat `json:"languages"`
}

func (r *Report) Summary() string {
	typeNames := make([]string, 0, len(r.Types))
	for t := range r.Types {
		typeNames = append(typeNames, t)
	}
	sort.Strings(typeNames)
	var b strings.Builder
	fmt.Fprintf(&b, "total=%d abstain=%d", r.Total, r.AbstainTotal)
	for _, t := range typeNames {
		st := r.Types[t]
		extra := ""
		if st.Count > 0 && st.AvgRecall > 0 {
			extra = fmt.Sprintf(" recall=%.2f", st.AvgRecall)
		}
		fmt.Fprintf(&b, " %s(n=%d abstain=%d cited=%d%s)", t, st.Count, st.Abstain, st.Cited, extra)
	}
	langNames := make([]string, 0, len(r.Languages))
	for lang := range r.Languages {
		langNames = append(langNames, lang)
	}
	sort.Strings(langNames)
	for _, lang := range langNames {
		st := r.Languages[lang]
		extra := ""
		if st.Count > 0 && st.AvgRecall > 0 {
			extra = fmt.Sprintf(" recall=%.2f", st.AvgRecall)
		}
		fmt.Fprintf(&b, " %s(n=%d abstain=%d cited=%d%s)", lang, st.Count, st.Abstain, st.Cited, extra)
	}
	return b.String()
}

const abstainVerdictText = "does not contain the information needed to answer"

var citationRe = regexp.MustCompile(`\([^()]+\)`)

func isAbstain(answer string) bool {
	return answer == report.NotFoundSentinel || strings.Contains(answer, abstainVerdictText)
}

func hasCitation(answer string) bool {
	return citationRe.MatchString(answer)
}

// FilterQuestions narrows the set by question types (nil/empty = all) and
// truncates to limit when positive.
func FilterQuestions(qs []corpus.Question, types map[string]struct{}, limit int) []corpus.Question {
	out := make([]corpus.Question, 0, len(qs))
	for _, q := range qs {
		if len(types) > 0 {
			if _, ok := types[q.Type]; !ok {
				continue
			}
		}
		out = append(out, q)
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out
}

func (r *Runner) Run(ctx context.Context) (*Report, error) {
	if r.Ask == nil {
		return nil, fmt.Errorf("bench: runner has no Ask function")
	}

	answers := make([]Answer, len(r.Questions))
	conc := r.Concurrency
	if conc <= 0 {
		conc = 1
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i, q := range r.Questions {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, q corpus.Question) {
			defer wg.Done()
			defer func() { <-sem }()
			text, docIDs := r.Ask(ctx, q)
			answers[i] = Answer{QuestionID: q.ID, Answer: text, DocumentIDs: CorpusDocumentIDs(docIDs)}
		}(i, q)
	}
	wg.Wait()

	if err := writeAnswers(r.OutPath, answers); err != nil {
		return nil, err
	}

	rep := buildReport(r.Questions, answers)
	return rep, nil
}

func writeAnswers(path string, answers []Answer) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("bench: create out dir: %w", err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("bench: create answers file: %w", err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, a := range answers {
		if err := enc.Encode(a); err != nil {
			return fmt.Errorf("bench: encode answer: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("bench: flush answers: %w", err)
	}
	return nil
}

func buildReport(qs []corpus.Question, answers []Answer) *Report {
	rep := &Report{
		Total:     len(answers),
		Types:     map[string]*TypeStat{},
		Languages: map[string]*TypeStat{},
	}
	typeRecallSums := map[string]float64{}
	typeRecallCounts := map[string]int{}
	langRecallSums := map[string]float64{}
	langRecallCounts := map[string]int{}

	for i, q := range qs {
		st, ok := rep.Types[q.Type]
		if !ok {
			st = &TypeStat{}
			rep.Types[q.Type] = st
		}
		st.Count++

		lang := q.Language
		if lang == "" {
			lang = "unknown"
		}
		langStat, ok := rep.Languages[lang]
		if !ok {
			langStat = &TypeStat{}
			rep.Languages[lang] = langStat
		}
		langStat.Count++

		a := answers[i]

		if isAbstain(a.Answer) {
			st.Abstain++
			langStat.Abstain++
			rep.AbstainTotal++
		}
		if hasCitation(a.Answer) {
			st.Cited++
			langStat.Cited++
		}
		if len(q.ExpectedDocIDs) > 0 {
			hit := make(map[string]struct{}, len(a.DocumentIDs))
			for _, id := range a.DocumentIDs {
				hit[id] = struct{}{}
			}
			matched := 0
			for _, want := range q.ExpectedDocIDs {
				if _, ok := hit[want]; ok {
					matched++
				}
			}
			recall := float64(matched) / float64(len(q.ExpectedDocIDs))
			typeRecallSums[q.Type] += recall
			typeRecallCounts[q.Type]++
			langRecallSums[lang] += recall
			langRecallCounts[lang]++
		}
	}
	for t, sum := range typeRecallSums {
		rep.Types[t].AvgRecall = sum / float64(typeRecallCounts[t])
	}
	for lang, sum := range langRecallSums {
		rep.Languages[lang].AvgRecall = sum / float64(langRecallCounts[lang])
	}
	return rep
}

func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func CorpusDocumentIDs(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, id := range in {
		if id == "" || isSyntheticBenchDocID(id) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func isSyntheticBenchDocID(id string) bool {
	return id == "set-summary" || strings.HasPrefix(id, "global:") || strings.HasPrefix(id, "community:")
}

// SaveReport writes the JSON report next to the answers file.
func SaveReport(path string, rep *Report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: encode report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("bench: write report: %w", err)
	}
	return nil
}
