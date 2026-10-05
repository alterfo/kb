package dynamic

import (
	"fmt"
	"strings"

	"github.com/alterfo/kb/internal/bench/run"
)

type State int

const (
	StateMissing State = iota
	StateCurrent
	StateStale
	StateHedged
)

func (s State) String() string {
	switch s {
	case StateCurrent:
		return "current"
	case StateStale:
		return "stale"
	case StateHedged:
		return "hedged"
	default:
		return "missing"
	}
}

type Case struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Language string   `json:"language"`
	Current  []string `json:"current"`
	Stale    []string `json:"stale,omitempty"`
	Control  bool     `json:"control,omitempty"`
}

type Report struct {
	Total            int     `json:"total"`
	Affected         int     `json:"affected"`
	Control          int     `json:"control"`
	CurrencyRate     float64 `json:"currency_rate"`
	StaleLeakRate    float64 `json:"stale_leak_rate"`
	HedgeRate        float64 `json:"hedge_rate"`
	MissRate         float64 `json:"miss_rate"`
	AdoptionEligible int     `json:"adoption_eligible"`
	Adoption         float64 `json:"adoption"`
	Stability        float64 `json:"stability"`
	DCS              float64 `json:"dcs"`
}

func mentions(answer string, facts []string, lang string) bool {
	for _, f := range facts {
		if run.GoldAnswerInText(answer, f, lang) {
			return true
		}
	}
	return false
}

func lead(answer string) string {
	for _, p := range strings.Split(answer, "\n\n") {
		if t := strings.TrimSpace(p); t != "" {
			return t
		}
	}
	return ""
}

func firstMention(text string, facts []string, lang string) int {
	words := strings.Fields(text)
	for k := 1; k <= len(words); k++ {
		if mentions(strings.Join(words[:k], " "), facts, lang) {
			return k
		}
	}
	return len(words) + 1
}

func Classify(answer string, c Case) State {
	answer = lead(answer)
	cur := mentions(answer, c.Current, c.Language)
	stale := mentions(answer, c.Stale, c.Language)
	switch {
	case cur && stale:
		if !c.Control && firstMention(answer, c.Current, c.Language) < firstMention(answer, c.Stale, c.Language) {
			return StateCurrent
		}
		return StateHedged
	case cur:
		return StateCurrent
	case stale:
		return StateStale
	default:
		return StateMissing
	}
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func Score(cases []Case, before, after map[string]string) *Report {
	r := &Report{Total: len(cases)}
	var cur, stale, hedge, miss, flipped, stable int
	for _, c := range cases {
		a := Classify(after[c.ID], c)
		if c.Control {
			r.Control++
			if a == Classify(before[c.ID], c) && a == StateCurrent {
				stable++
			}
			continue
		}
		r.Affected++
		switch a {
		case StateCurrent:
			cur++
		case StateStale:
			stale++
		case StateHedged:
			hedge++
		default:
			miss++
		}
		if Classify(before[c.ID], c) == StateStale {
			r.AdoptionEligible++
			if a == StateCurrent {
				flipped++
			}
		}
	}
	r.CurrencyRate = ratio(cur, r.Affected)
	r.StaleLeakRate = ratio(stale, r.Affected)
	r.HedgeRate = ratio(hedge, r.Affected)
	r.MissRate = ratio(miss, r.Affected)
	r.Adoption = ratio(flipped, r.AdoptionEligible)
	r.Stability = ratio(stable, r.Control)
	r.DCS = combine(r)
	return r
}

func combine(r *Report) float64 {
	if r.Control == 0 {
		return r.CurrencyRate
	}
	if r.Affected == 0 {
		return r.Stability
	}
	a := r.Adoption
	if r.AdoptionEligible == 0 {
		a = r.CurrencyRate
	}
	if a+r.Stability == 0 {
		return 0
	}
	return 2 * a * r.Stability / (a + r.Stability)
}

func (r *Report) Summary() string {
	return fmt.Sprintf("total=%d affected=%d control=%d currency=%.2f stale_leak=%.2f hedge=%.2f miss=%.2f adoption=%.2f (n=%d) stability=%.2f dcs=%.2f",
		r.Total, r.Affected, r.Control, r.CurrencyRate, r.StaleLeakRate, r.HedgeRate, r.MissRate, r.Adoption, r.AdoptionEligible, r.Stability, r.DCS)
}
