package retriever

import (
	"math"
	"strings"
)

// authorityBonus returns the bonus for the longest matching prefix of
// filePath in bonuses (e.g. "notes/approved/" outranks "notes/"). Zero if
// nothing matches.
func authorityBonus(filePath string, bonuses map[string]float64) float64 {
	best := 0.0
	bestLen := -1
	for prefix, bonus := range bonuses {
		if len(prefix) > bestLen && strings.HasPrefix(filePath, prefix) {
			best = bonus
			bestLen = len(prefix)
		}
	}
	return best
}

func supersededPenalty(supersededBy string) float64 {
	if supersededBy != "" {
		return 0.9
	}
	return 1
}

// minMaxNormalize rescales scores into [0, 1]. If every score is equal, all
// entries normalize to 1 rather than dividing by zero.
func minMaxNormalize(scores map[string]float64) map[string]float64 {
	normalized := make(map[string]float64, len(scores))
	if len(scores) == 0 {
		return normalized
	}

	min, max := math.Inf(1), math.Inf(-1)
	for _, v := range scores {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	if max == min {
		for id := range scores {
			normalized[id] = 1
		}
		return normalized
	}
	for id, v := range scores {
		normalized[id] = (v - min) / (max - min)
	}
	return normalized
}
