package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/store/vector"
)

type fakeFeedbackPrior struct {
	prior map[string]float64
	err   error
}

func (f *fakeFeedbackPrior) FeedbackByDoc(ctx context.Context) (map[string]float64, error) {
	return f.prior, f.err
}

func TestFeedbackPriorHelper(t *testing.T) {
	prior := map[string]float64{"doc-a": 2, "doc-b": -1}
	cases := []struct {
		doc  string
		want float64
	}{
		{"doc-a", 0.2},
		{"doc-b", -0.1},
		{"doc-missing", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := feedbackPrior(prior, 0.1, c.doc); got != c.want {
			t.Errorf("feedbackPrior(%q) = %v, want %v", c.doc, got, c.want)
		}
	}
	if got := feedbackPrior(prior, 0, "doc-a"); got != 0 {
		t.Errorf("zero bonus should disable the prior, got %v", got)
	}
	if got := feedbackPrior(nil, 0.1, "doc-a"); got != 0 {
		t.Errorf("nil prior should be zero, got %v", got)
	}
}

func TestFuseRankListsAppliesFeedbackPrior(t *testing.T) {
	chunkByID := map[string]vector.Chunk{
		"a": {ID: "a", RefDocID: "doc-up"},
		"b": {ID: "b", RefDocID: "doc-down"},
	}
	prior := &fakeFeedbackPrior{prior: map[string]float64{"doc-up": 3, "doc-down": -3}}
	r := New(Config{
		Feedback:      prior,
		FeedbackBonus: 0.1,
		RRFK:          60,
		PerDocCap:     10,
	})
	rankLists := [][]string{{"a", "b"}}
	scored := r.fuseRankLists(context.Background(), "q", Options{}, 10, chunkByID, rankLists)

	if len(scored) != 2 {
		t.Fatalf("got %d results, want 2", len(scored))
	}
	if scored[0].ID != "a" {
		t.Errorf("positive prior should outrank negative: got %q first", scored[0].ID)
	}
	if scored[0].Score <= scored[1].Score {
		t.Errorf("expected distinct scores, got %v and %v", scored[0].Score, scored[1].Score)
	}
}

func TestFuseRankListsFeedbackErrorFailOpen(t *testing.T) {
	chunkByID := map[string]vector.Chunk{
		"a": {ID: "a", RefDocID: "doc-a"},
		"b": {ID: "b", RefDocID: "doc-b"},
	}
	r := New(Config{
		Feedback:      &fakeFeedbackPrior{err: errors.New("boom")},
		FeedbackBonus: 0.1,
		RRFK:          60,
		PerDocCap:     10,
	})
	scored := r.fuseRankLists(context.Background(), "q", Options{}, 10, chunkByID, [][]string{{"a", "b"}})
	if len(scored) != 2 {
		t.Fatalf("feedback error should fail open and return results, got %d", len(scored))
	}
}
