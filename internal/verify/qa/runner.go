package qa

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Answer struct {
	Text    string
	Sources []string
}

type AskFunc func(ctx context.Context, question string) (Answer, error)

type SourceMetric struct {
	Total  int
	Passed int
}

func (m SourceMetric) Rate() float64 {
	if m.Total == 0 {
		return 0
	}
	return float64(m.Passed) / float64(m.Total)
}

type Result struct {
	ID         string   `json:"id,omitempty"`
	Question   string   `json:"question"`
	Answer     string   `json:"answer,omitempty"`
	Expected   string   `json:"expected,omitempty"`
	Passed     bool     `json:"passed"`
	Reason     string   `json:"reason,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	Overlap    float64  `json:"overlap,omitempty"`
	AskError   string   `json:"ask_error,omitempty"`
	JudgeError string   `json:"judge_error,omitempty"`
}

type Report struct {
	GeneratedAt time.Time               `json:"generated_at"`
	SampleSize  int                     `json:"sample_size"`
	Asked       int                     `json:"asked"`
	Passed      int                     `json:"passed"`
	AskErrors   int                     `json:"ask_errors"`
	JudgeErrors int                     `json:"judge_errors"`
	BySource    map[string]SourceMetric `json:"by_source,omitempty"`
	Results     []Result                `json:"results"`
}

func (r Report) Rate() float64 {
	if r.Asked == 0 {
		return 0
	}
	return float64(r.Passed) / float64(r.Asked)
}

type Runner struct {
	Ask   AskFunc
	Judge Judge
}

func (r *Runner) Run(ctx context.Context, pairs []QAPair) (Report, error) {
	rep := Report{
		GeneratedAt: time.Now().UTC(),
		SampleSize:  len(pairs),
		BySource:    make(map[string]SourceMetric),
	}
	if r == nil || r.Ask == nil {
		return Report{}, fmt.Errorf("qa: Ask is required")
	}
	for _, pair := range pairs {
		res := Result{ID: pair.ID, Question: pair.Question, Expected: pair.Expected}
		ans, err := r.Ask(ctx, pair.Question)
		if err != nil {
			res.AskError = err.Error()
			rep.AskErrors++
			rep.Results = append(rep.Results, res)
			continue
		}
		rep.Asked++
		res.Answer = ans.Text
		res.Sources = uniqueStrings(ans.Sources)
		if r.Judge == nil {
			res.Passed = Overlap(ans.Text, pair.Expected) >= DefaultOverlapThreshold
			res.Reason = "overlap fallback: judge unavailable"
			res.Overlap = Overlap(ans.Text, pair.Expected)
		} else {
			res.Overlap = Overlap(ans.Text, pair.Expected)
			verdict, err := r.Judge.Judge(ctx, pair.Question, ans.Text, pair.Expected)
			if err != nil {
				res.JudgeError = err.Error()
				res.Passed = res.Overlap >= DefaultOverlapThreshold
				res.Reason = "overlap fallback after judge error"
				rep.JudgeErrors++
			} else {
				res.Passed = verdict.Passed
				res.Reason = verdict.Reason
			}
		}
		if res.Passed {
			rep.Passed++
		}
		for _, source := range res.Sources {
			m := rep.BySource[source]
			m.Total++
			if res.Passed {
				m.Passed++
			}
			rep.BySource[source] = m
		}
		rep.Results = append(rep.Results, res)
	}
	return rep, nil
}

func (r Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "QA hit-rate: %.3f (%d/%d)\n", r.Rate(), r.Passed, r.Asked)
	fmt.Fprintf(&b, "sample size: %d, ask errors: %d, judge errors: %d", r.SampleSize, r.AskErrors, r.JudgeErrors)
	if len(r.BySource) == 0 {
		return b.String()
	}
	sources := make([]string, 0, len(r.BySource))
	for source := range r.BySource {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	b.WriteString("\nby source:")
	for _, source := range sources {
		m := r.BySource[source]
		fmt.Fprintf(&b, "\n  %s: %.3f (%d/%d)", source, m.Rate(), m.Passed, m.Total)
	}
	return b.String()
}

func (r Report) JSON() ([]byte, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("qa: encode report: %w", err)
	}
	return append(data, '\n'), nil
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
