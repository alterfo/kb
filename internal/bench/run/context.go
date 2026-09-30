package run

import (
	"strings"

	"github.com/alterfo/kb/internal/bench/corpus"
)

type ContextChunk struct {
	DocID string `json:"doc_id,omitempty"`
	Text  string `json:"text,omitempty"`
}

func contextGoldFacts(q corpus.Question) []string {
	if len(q.AnswerFacts) > 0 {
		return q.AnswerFacts
	}
	if strings.TrimSpace(q.GoldAnswer) != "" {
		return []string{q.GoldAnswer}
	}
	return nil
}

func contextStemSet(texts []string, lang string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, t := range texts {
		for _, s := range factContentStems(t, lang) {
			set[s] = struct{}{}
		}
	}
	return set
}

func factsStemSet(facts []string, lang string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, f := range facts {
		for _, s := range factContentStems(f, lang) {
			set[s] = struct{}{}
		}
	}
	return set
}

func stemIntersection(a, b map[string]struct{}) int {
	n := 0
	for s := range a {
		if _, ok := b[s]; ok {
			n++
		}
	}
	return n
}

func chunkTexts(chunks []ContextChunk) []string {
	texts := make([]string, 0, len(chunks))
	for _, c := range chunks {
		texts = append(texts, c.Text)
	}
	return texts
}

func ContextPrecision(chunks []ContextChunk, facts []string, lang string) float64 {
	if len(facts) == 0 {
		return 0
	}
	ctxSet := contextStemSet(chunkTexts(chunks), lang)
	if len(ctxSet) == 0 {
		return 0
	}
	return float64(stemIntersection(ctxSet, factsStemSet(facts, lang))) / float64(len(ctxSet))
}

func ContextRecall(chunks []ContextChunk, facts []string, lang string) float64 {
	if len(facts) == 0 {
		return 0
	}
	factSet := factsStemSet(facts, lang)
	if len(factSet) == 0 {
		return 0
	}
	return float64(stemIntersection(factSet, contextStemSet(chunkTexts(chunks), lang))) / float64(len(factSet))
}
