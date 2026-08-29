package corpus

import (
	"path/filepath"
	"testing"
)

func TestLangBenchDatasetLoadsCleanly(t *testing.T) {
	corpusRoot := filepath.Join("..", "..", "..", "testdata", "lang-bench", "corpus")
	questionsPath := filepath.Join("..", "..", "..", "testdata", "lang-bench", "questions.jsonl")

	docs, warns, err := LoadCorpus(corpusRoot)
	if err != nil {
		t.Fatalf("LoadCorpus() error = %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("LoadCorpus() warnings = %v, want none", warns)
	}
	if len(docs) != 16 {
		t.Fatalf("LoadCorpus() count = %d, want 16", len(docs))
	}

	qs, qwarns, err := LoadQuestions(questionsPath)
	if err != nil {
		t.Fatalf("LoadQuestions() error = %v", err)
	}
	if len(qwarns) != 0 {
		t.Fatalf("LoadQuestions() warnings = %v, want none", qwarns)
	}
	if len(qs) != 20 {
		t.Fatalf("LoadQuestions() count = %d, want 20", len(qs))
	}

	docIDs := make(map[string]struct{}, len(docs))
	for _, d := range docs {
		docIDs[d.ID] = struct{}{}
	}
	for _, q := range qs {
		for _, want := range q.ExpectedDocIDs {
			if _, ok := docIDs[want]; !ok {
				t.Errorf("question %s references missing doc id %q", q.ID, want)
			}
		}
	}

	langCounts := map[string]int{}
	for _, q := range qs {
		langCounts[q.Language]++
	}
	for _, tc := range []struct {
		lang string
		want int
	}{
		{lang: "ru", want: 10},
		{lang: "en", want: 10},
	} {
		if got := langCounts[tc.lang]; got != tc.want {
			t.Errorf("language %q count = %d, want %d", tc.lang, got, tc.want)
		}
	}
}
