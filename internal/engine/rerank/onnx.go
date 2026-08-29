package rerank

import (
	"context"
	"errors"

	"github.com/alterfo/kb/internal/store/vector"
)

// ErrONNXNotImplemented is returned by ONNX.Rerank; the backend is a
// placeholder for a future cross-encoder (e.g. bge-reranker) and is not
// wired up in the prototype. Callers fail open to the input order.
var ErrONNXNotImplemented = errors.New("rerank: onnx backend not implemented")

// ONNX is a pluggable stub for a future local cross-encoder reranker.
type ONNX struct{}

func (ONNX) Rerank(ctx context.Context, query string, cands []vector.ScoredChunk) ([]vector.ScoredChunk, error) {
	return cands, ErrONNXNotImplemented
}
