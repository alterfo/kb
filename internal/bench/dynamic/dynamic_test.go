package dynamic

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestClassify(t *testing.T) {
	c := Case{ID: "q", Language: "en", Current: []string{"Berlin"}, Stale: []string{"Munich"}}
	tests := []struct {
		name   string
		answer string
		want   State
	}{
		{"current only", "The office is in Berlin.", StateCurrent},
		{"stale only", "The office is in Munich.", StateStale},
		{"both stale first", "It moved from Munich to Berlin.", StateHedged},
		{"both current first", "The office is in Berlin (earlier it was Munich).", StateCurrent},
		{"both current first across sentences", "Berlin is the office. Munich was the old one.", StateCurrent},
		{"both stale first across sentences", "Munich was the office. Berlin is the new one.", StateHedged},
		{"neither", "I do not know.", StateMissing},
		{"empty", "", StateMissing},
		{"stale only in supporting facts", "The office is in Berlin.\n\nSupporting facts:\n- the old doc said Munich", StateCurrent},
		{"leading blank paragraphs", "\n\n  The office is in Munich.\n\nBerlin", StateStale},
	}
	for _, tt := range tests {
		if got := Classify(tt.answer, c); got != tt.want {
			t.Errorf("%s: got %v want %v", tt.name, got, tt.want)
		}
	}
}

func TestClassifyControlHasNoStale(t *testing.T) {
	c := Case{ID: "c", Language: "en", Current: []string{"blue"}, Control: true}
	if got := Classify("It is blue.", c); got != StateCurrent {
		t.Fatalf("got %v", got)
	}
	if got := Classify("unknown", c); got != StateMissing {
		t.Fatalf("got %v", got)
	}
}

func TestClassifyControlOrderDoesNotExcuseContamination(t *testing.T) {
	c := Case{ID: "c", Language: "en", Current: []string{"blue"}, Stale: []string{"seven"}, Control: true}
	if got := Classify("It is blue, and also seven.", c); got != StateHedged {
		t.Fatalf("got %v", got)
	}
}

func TestScore(t *testing.T) {
	cases := []Case{
		{ID: "a1", Language: "en", Current: []string{"Berlin"}, Stale: []string{"Munich"}},
		{ID: "a2", Language: "en", Current: []string{"Friday"}, Stale: []string{"Monday"}},
		{ID: "a3", Language: "en", Current: []string{"red"}, Stale: []string{"green"}},
		{ID: "a4", Language: "en", Current: []string{"seven"}, Stale: []string{"five"}},
		{ID: "c1", Language: "en", Current: []string{"blue"}, Control: true},
		{ID: "c2", Language: "en", Current: []string{"oak"}, Control: true},
	}
	before := map[string]string{
		"a1": "Munich", "a2": "Monday", "a3": "green", "a4": "five",
		"c1": "blue", "c2": "oak",
	}
	after := map[string]string{
		"a1": "Berlin",
		"a2": "Monday",
		"a3": "green and red",
		"a4": "unknown",
		"c1": "blue",
		"c2": "pine",
	}
	r := Score(cases, before, after)
	if r.Affected != 4 || r.Control != 2 {
		t.Fatalf("counts: %+v", r)
	}
	if !approx(r.CurrencyRate, 0.25) {
		t.Errorf("currency %v", r.CurrencyRate)
	}
	if !approx(r.StaleLeakRate, 0.25) {
		t.Errorf("stale %v", r.StaleLeakRate)
	}
	if !approx(r.HedgeRate, 0.25) {
		t.Errorf("hedge %v", r.HedgeRate)
	}
	if !approx(r.MissRate, 0.25) {
		t.Errorf("miss %v", r.MissRate)
	}
	if r.AdoptionEligible != 4 || !approx(r.Adoption, 0.25) {
		t.Errorf("adoption %v eligible %d", r.Adoption, r.AdoptionEligible)
	}
	if !approx(r.Stability, 0.5) {
		t.Errorf("stability %v", r.Stability)
	}
	want := 2 * 0.25 * 0.5 / (0.25 + 0.5)
	if !approx(r.DCS, want) {
		t.Errorf("dcs %v want %v", r.DCS, want)
	}
}

func TestScoreAdoptionOnlyCountsStaleBefore(t *testing.T) {
	cases := []Case{
		{ID: "a1", Language: "en", Current: []string{"Berlin"}, Stale: []string{"Munich"}},
		{ID: "a2", Language: "en", Current: []string{"Friday"}, Stale: []string{"Monday"}},
	}
	before := map[string]string{"a1": "Munich", "a2": "unknown"}
	after := map[string]string{"a1": "Berlin", "a2": "Friday"}
	r := Score(cases, before, after)
	if r.AdoptionEligible != 1 || !approx(r.Adoption, 1) {
		t.Fatalf("adoption %v eligible %d", r.Adoption, r.AdoptionEligible)
	}
	if !approx(r.CurrencyRate, 1) {
		t.Fatalf("currency %v", r.CurrencyRate)
	}
}

func TestScoreEmptyAndMissingAnswers(t *testing.T) {
	r := Score(nil, nil, nil)
	if r.Total != 0 || r.DCS != 0 {
		t.Fatalf("%+v", r)
	}
	cases := []Case{{ID: "a", Language: "en", Current: []string{"x1"}, Stale: []string{"y1"}}}
	r = Score(cases, nil, nil)
	if r.Total != 1 || r.MissRate != 1 || r.DCS != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestScoreNoControlDCSEqualsCurrency(t *testing.T) {
	cases := []Case{{ID: "a", Language: "en", Current: []string{"Berlin"}, Stale: []string{"Munich"}}}
	r := Score(cases, map[string]string{"a": "Munich"}, map[string]string{"a": "Berlin"})
	if !approx(r.DCS, 1) {
		t.Fatalf("dcs %v", r.DCS)
	}
}

func TestSummary(t *testing.T) {
	r := Score([]Case{{ID: "a", Language: "en", Current: []string{"Berlin"}, Stale: []string{"Munich"}}},
		map[string]string{"a": "Munich"}, map[string]string{"a": "Berlin"})
	if s := r.Summary(); s == "" {
		t.Fatal("empty summary")
	}
}
