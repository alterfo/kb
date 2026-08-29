package retriever

import (
	"context"
	"sort"

	"github.com/alterfo/kb/internal/store/vector"
)

// expandIntraDoc pulls the remaining sections of winning documents into the
// result set so synthesis can reason across distant parts of one document
// (intra-document questions defeat naive chunk-level retrieval). Sections
// are added in chunk order, capped by an approximate token budget and by K.
func (r *Retriever) expandIntraDoc(ctx context.Context, scored []vector.ScoredChunk, k int, filter vector.Filter) []vector.ScoredChunk {
	if r.cfg.IntraDocBudget <= 0 || len(scored) == 0 {
		return scored
	}

	present := make(map[string]struct{}, len(scored))
	tokens := 0
	for _, sc := range scored {
		present[sc.Chunk.ID] = struct{}{}
		tokens += estimateTokens(sc.Chunk.Text)
	}

	out := append([]vector.ScoredChunk(nil), scored...)
	for _, sc := range scored {
		if tokens >= r.cfg.IntraDocBudget || len(out) >= k {
			break
		}
		chunks, err := r.cfg.Vector.ChunksByDoc(ctx, sc.Chunk.RefDocID)
		if err != nil {
			continue
		}
		sortByChunkIndex(chunks)
		for _, c := range chunks {
			if c.ValidTo != "" {
				continue
			}
			if !filter.Matches(c.Source, c.Metadata) {
				continue
			}
			if tokens >= r.cfg.IntraDocBudget || len(out) >= k {
				break
			}
			if _, ok := present[c.ID]; ok {
				continue
			}
			tokens += estimateTokens(c.Text)
			present[c.ID] = struct{}{}
			out = append(out, vector.ScoredChunk{Chunk: c, Score: sc.Score})
		}
	}
	return out
}

func sortByChunkIndex(chunks []vector.Chunk) {
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].ChunkIndex < chunks[j].ChunkIndex })
}

func estimateTokens(text string) int {
	n := len(text) / 4
	if n < 1 {
		n = 1
	}
	return n
}
