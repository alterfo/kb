package retriever

import "testing"

func TestRRFScoresFusesAcrossLists(t *testing.T) {
	lists := [][]string{
		{"a", "b", "c"},
		{"b", "a"},
	}
	scores := rrfScores(lists, 60)

	// a: rank 0 in list 1, rank 1 in list 2. b: rank 1 in list 1, rank 0 in
	// list 2. Same rank multiset {0,1}, so they fuse to an equal score.
	wantAB := 1.0/61 + 1.0/62
	wantC := 1.0 / 63

	if abs(scores["a"]-wantAB) > 1e-9 {
		t.Errorf("score(a) = %v, want %v", scores["a"], wantAB)
	}
	if abs(scores["b"]-wantAB) > 1e-9 {
		t.Errorf("score(b) = %v, want %v", scores["b"], wantAB)
	}
	if abs(scores["c"]-wantC) > 1e-9 {
		t.Errorf("score(c) = %v, want %v", scores["c"], wantC)
	}
	if scores["a"] <= scores["c"] {
		t.Errorf("expected a/b (appear in both lists) to outscore c (appears once): a=%v c=%v", scores["a"], scores["c"])
	}
}

func TestRRFScoresOnlyIncludesSeenIDs(t *testing.T) {
	scores := rrfScores([][]string{{"x"}}, 60)
	if len(scores) != 1 {
		t.Fatalf("got %d scores, want 1", len(scores))
	}
	if _, ok := scores["y"]; ok {
		t.Fatal("unseen id should not appear in scores")
	}
}

func TestRRFScoresEmptyInput(t *testing.T) {
	if scores := rrfScores(nil, 60); len(scores) != 0 {
		t.Fatalf("expected empty scores for nil input, got %v", scores)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
