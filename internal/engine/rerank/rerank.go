package rerank

import (
	"context"

	"github.com/alterfo/kb/internal/store/vector"
)

// Reranker reorders retrieval candidates by relevance to query. Every
// implementation is fail-open: on any internal failure it returns the
// input order unchanged rather than erroring the caller's pipeline.
type Reranker interface {
	Rerank(ctx context.Context, query string, cands []vector.ScoredChunk) ([]vector.ScoredChunk, error)
}

// Noop returns candidates in their input order, unchanged. Used for
// KB_RERANK=off.
type Noop struct{}

func (Noop) Rerank(ctx context.Context, query string, cands []vector.ScoredChunk) ([]vector.ScoredChunk, error) {
	return cands, nil
}
