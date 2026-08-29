package legaleval

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Citation struct {
	FileName string
	FilePath string
	ChunkID  string
}

type Answer struct {
	Text      string
	Citations []Citation
}

type AskFunc func(ctx context.Context, question string) (Answer, error)

type Verdict struct {
	Passed bool
	Detail string
}

type Judge interface {
	StatuteRelevant(ctx context.Context, question string, article Article) (Verdict, error)
	ClaimTruthful(ctx context.Context, question, answer string, evidence []string) (Verdict, error)
}

type Metric struct {
	Total  int
	Passed int
}

func (m Metric) Rate() float64 {
	if m.Total == 0 {
		return 0
	}
	return float64(m.Passed) / float64(m.Total)
}

type AnswerResult struct {
	Question      string
	Answer        string
	Citations     []Citation
	CitedStatutes []string
	NHSR          Metric
	SRR           Metric
	LCT           Verdict
	AskError      string
	JudgeErrors   []string
}

type Report struct {
	Results     []AnswerResult
	NHSR        Metric
	SRR         Metric
	LCT         Metric
	AskErrors   int
	JudgeErrors int
}

func (r Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Non-Hallucinated-Statute-Rate: %.3f (%d/%d)\n", r.NHSR.Rate(), r.NHSR.Passed, r.NHSR.Total)
	fmt.Fprintf(&b, "Statute-Relevance-Rate: %.3f (%d/%d)\n", r.SRR.Rate(), r.SRR.Passed, r.SRR.Total)
	fmt.Fprintf(&b, "Legal-Claim-Truthfulness: %.3f (%d/%d)\n", r.LCT.Rate(), r.LCT.Passed, r.LCT.Total)
	fmt.Fprintf(&b, "ask errors: %d, judge errors: %d", r.AskErrors, r.JudgeErrors)
	return b.String()
}

type Eval struct {
	Ask     AskFunc
	Judge   Judge
	Corpus  *Corpus
	Plenum  *Plenum
	AsOf    time.Time
	Resolve func(ctx context.Context, c Citation) (string, bool)
}

func (e *Eval) Run(ctx context.Context, pairs []QAPair) (Report, error) {
	if e == nil || e.Ask == nil || e.Corpus == nil {
		return Report{}, fmt.Errorf("legaleval: Ask and Corpus are required")
	}
	asOf := e.AsOf
	if asOf.IsZero() {
		asOf = e.Corpus.AsOf()
	}
	var rep Report
	for _, p := range pairs {
		res := AnswerResult{Question: p.Question}
		ans, err := e.Ask(ctx, p.Question)
		if err != nil {
			res.AskError = err.Error()
			rep.AskErrors++
			rep.Results = append(rep.Results, res)
			continue
		}
		res.Answer = ans.Text
		res.Citations = ans.Citations
		seen := map[string]bool{}
		for _, c := range ans.Citations {
			id, ok := e.resolve(ctx, c)
			if !ok {
				res.NHSR.Total++
				continue
			}
			if _, known := e.Corpus.Article(id); !known {
				if e.Plenum != nil {
					if e.Plenum.Known(id) {
						continue
					}
				}
				res.NHSR.Total++
				continue
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			res.CitedStatutes = append(res.CitedStatutes, id)
		}

		for _, id := range res.CitedStatutes {
			res.NHSR.Total++
			if e.Corpus.CurrentAt(id, asOf) {
				res.NHSR.Passed++
			}
		}

		if e.Judge != nil {
			for _, id := range res.CitedStatutes {
				article, ok := e.Corpus.Article(id)
				if !ok {
					continue
				}
				v, err := e.Judge.StatuteRelevant(ctx, p.Question, article)
				if err != nil {
					res.JudgeErrors = append(res.JudgeErrors, id+": "+err.Error())
					rep.JudgeErrors++
					continue
				}
				res.SRR.Total++
				if v.Passed {
					res.SRR.Passed++
				}
			}
			v, err := e.Judge.ClaimTruthful(ctx, p.Question, ans.Text, e.evidence(p))
			if err != nil {
				res.JudgeErrors = append(res.JudgeErrors, "claim truthfulness: "+err.Error())
				rep.JudgeErrors++
			} else {
				res.LCT = v
				rep.LCT.Total++
				if v.Passed {
					rep.LCT.Passed++
				}
			}
		}

		rep.NHSR.Total += res.NHSR.Total
		rep.NHSR.Passed += res.NHSR.Passed
		rep.SRR.Total += res.SRR.Total
		rep.SRR.Passed += res.SRR.Passed
		rep.Results = append(rep.Results, res)
	}
	return rep, nil
}

func (e *Eval) resolve(ctx context.Context, c Citation) (string, bool) {
	if e.Resolve != nil {
		return e.Resolve(ctx, c)
	}
	return e.Corpus.Resolve(c)
}

func (e *Eval) evidence(p QAPair) []string {
	var out []string
	for _, id := range p.ExpectedArticles {
		if a, ok := e.Corpus.Article(id); ok {
			out = append(out, "Статья "+id+": "+a.Body)
		}
	}
	if e.Plenum != nil {
		for _, id := range p.ExpectedPlenumPoints {
			if pt, ok := e.Plenum.Point(id); ok {
				out = append(out, id+": "+pt.Body)
			}
		}
	}
	return out
}
