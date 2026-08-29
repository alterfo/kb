package retriever

import "github.com/alterfo/kb/internal/store/vector"

// SupersedeMode controls how superseded chunks interact with their
// replacements during fusion.
type SupersedeMode string

const (
	// SupersedeSoft keeps both versions, ranking the older one lower via
	// the superseded penalty (default).
	SupersedeSoft SupersedeMode = "soft"
	// SupersedeStrict drops a superseded chunk when its replacing document
	// is present in the same candidate set; without the replacement the
	// old version still surfaces so answers never go empty.
	SupersedeStrict SupersedeMode = "strict"
)

// dropSupersededWithNewerPresent removes chunks whose superseding document
// is also part of the candidate set, keeping the rest untouched.
func dropSupersededWithNewerPresent(scored []vector.ScoredChunk) []vector.ScoredChunk {
	docs := make(map[string]struct{}, len(scored))
	for _, sc := range scored {
		docs[sc.Chunk.RefDocID] = struct{}{}
	}
	out := scored[:0]
	for _, sc := range scored {
		if _, newer := docs[sc.Chunk.SupersededBy]; sc.Chunk.SupersededBy != "" && newer {
			continue
		}
		out = append(out, sc)
	}
	return out
}
