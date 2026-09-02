package dragon

import (
	"testing"
	"time"

	"github.com/alterfo/kb/internal/bench/corpus"
)

func TestLimitTexts(t *testing.T) {
	texts := []Text{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3}}

	if got := LimitTexts(texts, 2); len(got) != 2 || got[0].ID != 0 || got[1].ID != 1 {
		t.Fatalf("LimitTexts(2) = %+v", got)
	}
	if got := LimitTexts(texts, 0); len(got) != 4 {
		t.Fatalf("LimitTexts(0) = %d, want 4", len(got))
	}
	if got := LimitTexts(texts, 10); len(got) != 4 {
		t.Fatalf("LimitTexts(10) = %d, want 4", len(got))
	}
	if got := LimitTexts(texts, -1); len(got) != 4 {
		t.Fatalf("LimitTexts(-1) = %d, want 4", len(got))
	}
	if got := LimitTexts(nil, 2); len(got) != 0 {
		t.Fatalf("LimitTexts(nil) = %d, want 0", len(got))
	}
}

func TestTextIDSet(t *testing.T) {
	texts := []Text{{ID: 10}, {ID: 20}}
	set := TextIDSet(texts)
	if len(set) != 2 {
		t.Fatalf("len(TextIDSet) = %d, want 2", len(set))
	}
	if _, ok := set["10"]; !ok {
		t.Errorf("TextIDSet missing 10")
	}
	if _, ok := set["20"]; !ok {
		t.Errorf("TextIDSet missing 20")
	}
}

func TestFilterQuestions(t *testing.T) {
	gold := []GoldQA{
		{PublicID: 1, TextIDs: "[10, 20]"}, // fully contained
		{PublicID: 2, TextIDs: "[10, 30]"}, // partially contained (30 missing)
		{PublicID: 3, TextIDs: "[10,"},     // malformed
		{PublicID: 4, TextIDs: "[]"},       // empty source set
		{PublicID: 5, TextIDs: "[40]"},     // fully contained
	}
	kept := map[string]struct{}{"10": {}, "20": {}, "40": {}}
	questions := []corpus.Question{{ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"}, {ID: "5"}, {ID: "6"}}

	matched, malformed := FilterQuestions(gold, questions, kept)
	if malformed != 1 {
		t.Fatalf("malformed = %d, want 1", malformed)
	}
	if len(matched) != 2 {
		t.Fatalf("len(matched) = %d, want 2: %+v", len(matched), matched)
	}
	if matched[0].ID != "1" || matched[1].ID != "5" {
		t.Errorf("matched = %+v, want question IDs [1 5]", matched)
	}
}

func TestFilterQuestions_NoKeptDocs(t *testing.T) {
	gold := []GoldQA{{PublicID: 1, TextIDs: "[10]"}}
	questions := []corpus.Question{{ID: "1"}}
	matched, malformed := FilterQuestions(gold, questions, map[string]struct{}{})
	if malformed != 0 {
		t.Fatalf("malformed = %d, want 0", malformed)
	}
	if len(matched) != 0 {
		t.Fatalf("len(matched) = %d, want 0", len(matched))
	}
}

func TestFilterQuestions_AllMalformed(t *testing.T) {
	gold := []GoldQA{{PublicID: 1, TextIDs: "not json"}}
	questions := []corpus.Question{{ID: "1"}}
	matched, malformed := FilterQuestions(gold, questions, map[string]struct{}{})
	if malformed != 1 {
		t.Fatalf("malformed = %d, want 1", malformed)
	}
	if len(matched) != 0 {
		t.Fatalf("len(matched) = %d, want 0", len(matched))
	}
}

func TestMaxDocLimit(t *testing.T) {
	cases := []struct {
		name          string
		secondsPerDoc float64
		budget        time.Duration
		want          int
	}{
		{"exact fit", 10, 100 * time.Second, 10},
		{"floor", 17, 45 * time.Minute, 158},
		{"rate exceeds budget", 200, 100 * time.Second, 0},
		{"zero rate", 0, time.Minute, 0},
		{"negative budget", 10, -time.Minute, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaxDocLimit(tc.secondsPerDoc, tc.budget); got != tc.want {
				t.Errorf("MaxDocLimit(%v, %v) = %d, want %d", tc.secondsPerDoc, tc.budget, got, tc.want)
			}
		})
	}
}
