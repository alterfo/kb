package retriever

// rrfScores fuses ranked id lists via Reciprocal Rank Fusion:
// score(id) = Σ 1/(k + rank + 1) over every list containing id, rank 0-indexed.
func rrfScores(rankLists [][]string, k int) map[string]float64 {
	scores := make(map[string]float64)
	for _, ids := range rankLists {
		for rank, id := range ids {
			scores[id] += 1.0 / float64(k+rank+1)
		}
	}
	return scores
}
