package retriever

import "testing"

func TestAuthorityBonusLongestPrefixWins(t *testing.T) {
	bonuses := map[string]float64{
		"notes/":          0.15,
		"notes/approved/": 0.30,
	}
	cases := []struct {
		path string
		want float64
	}{
		{"notes/approved/decision.md", 0.30},
		{"notes/draft.md", 0.15},
		{"chats/slack/general.md", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := authorityBonus(c.path, bonuses); got != c.want {
			t.Errorf("authorityBonus(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestAuthorityBonusNilMap(t *testing.T) {
	if got := authorityBonus("notes/approved/x.md", nil); got != 0 {
		t.Errorf("authorityBonus with nil bonuses = %v, want 0", got)
	}
}

func TestMinMaxNormalizeRescalesToUnitRange(t *testing.T) {
	scores := map[string]float64{"a": 1, "b": 3, "c": 2}
	got := minMaxNormalize(scores)
	if got["a"] != 0 {
		t.Errorf("min should normalize to 0, got %v", got["a"])
	}
	if got["b"] != 1 {
		t.Errorf("max should normalize to 1, got %v", got["b"])
	}
	if got["c"] != 0.5 {
		t.Errorf("midpoint should normalize to 0.5, got %v", got["c"])
	}
}

func TestMinMaxNormalizeAllEqualAvoidsDivByZero(t *testing.T) {
	got := minMaxNormalize(map[string]float64{"a": 5, "b": 5})
	if got["a"] != 1 || got["b"] != 1 {
		t.Errorf("equal scores should all normalize to 1, got %v", got)
	}
}

func TestMinMaxNormalizeEmpty(t *testing.T) {
	if got := minMaxNormalize(nil); len(got) != 0 {
		t.Errorf("expected empty result for empty input, got %v", got)
	}
}
