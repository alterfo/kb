package rerank

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/store/vector"
)

func TestNoopReturnsInputOrderUnchanged(t *testing.T) {
	cands := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "a"}, Score: 0.5},
		{Chunk: vector.Chunk{ID: "b"}, Score: 0.9},
	}

	got, err := Noop{}.Rerank(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != 2 || got[0].Chunk.ID != "a" || got[1].Chunk.ID != "b" {
		t.Fatalf("got %+v, want unchanged order", got)
	}
}
