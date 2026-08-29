package rerank

import (
	"context"
	"errors"
	"testing"
)

func TestONNXNotImplementedFailsOpen(t *testing.T) {
	cands := candidates("a", "b")

	got, err := (ONNX{}).Rerank(context.Background(), "q", cands)
	if !errors.Is(err, ErrONNXNotImplemented) {
		t.Fatalf("err = %v, want ErrONNXNotImplemented", err)
	}
	if !sameOrder(got, cands) {
		t.Fatalf("got %v, want unchanged order %v", got, cands)
	}
}

var _ Reranker = ONNX{}
var _ Reranker = Noop{}
var _ Reranker = (*LLM)(nil)
