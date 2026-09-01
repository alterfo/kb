package actualize

import (
	"testing"

	"github.com/alterfo/kb/internal/bench/dragon"
)

func TestFixtureCounts(t *testing.T) {
	if got := len(SeedDocs()); got != 10 {
		t.Fatalf("len(SeedDocs()) = %d, want 10", got)
	}
	if got := len(ChatCorrections()); got != 5 {
		t.Fatalf("len(ChatCorrections()) = %d, want 5", got)
	}
	qs := Questions()
	if got := len(qs); got != 15 {
		t.Fatalf("len(Questions()) = %d, want 15", got)
	}
	var affected, control int
	for _, q := range qs {
		if q.Affected {
			affected++
		} else {
			control++
		}
	}
	if affected != 10 || control != 5 {
		t.Fatalf("affected/control = %d/%d, want 10/5", affected, control)
	}
}

func TestSeedDocIDsUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for _, d := range SeedDocs() {
		if _, ok := seen[d.ID]; ok {
			t.Errorf("duplicate seed doc ID %q", d.ID)
		}
		seen[d.ID] = struct{}{}
	}
}

func TestGoldAnswersStemMatchSources(t *testing.T) {
	docs := make(map[string]SeedDoc, len(SeedDocs()))
	for _, d := range SeedDocs() {
		docs[d.ID] = d
	}
	corrections := make(map[string]ChatCorrection, len(ChatCorrections()))
	for _, c := range ChatCorrections() {
		corrections[c.CorrectsDocID] = c
	}

	for _, q := range Questions() {
		doc, ok := docs[q.TargetDocID]
		if !ok {
			t.Errorf("question %q targets unknown seed doc %q", q.Question, q.TargetDocID)
			continue
		}
		if !dragon.AnswerContainsGold(doc.Body, q.AnswerBefore) {
			t.Errorf("answerBefore %q does not stem-match seed doc %q body %q", q.AnswerBefore, q.TargetDocID, doc.Body)
		}
		if !q.Affected {
			if q.AnswerAfter != q.AnswerBefore {
				t.Errorf("control question %q has answerAfter %q != answerBefore %q", q.Question, q.AnswerAfter, q.AnswerBefore)
			}
			continue
		}
		corr, ok := corrections[q.TargetDocID]
		if !ok {
			t.Errorf("affected question %q targets doc %q with no correction", q.Question, q.TargetDocID)
			continue
		}
		if !dragon.AnswerContainsGold(corr.Text, q.AnswerAfter) {
			t.Errorf("answerAfter %q does not stem-match correction text %q", q.AnswerAfter, corr.Text)
		}
		if q.AnswerAfter == q.AnswerBefore {
			t.Errorf("affected question %q has identical before/after answers %q", q.Question, q.AnswerBefore)
		}
	}
}
