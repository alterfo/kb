package metrics

import (
	"testing"
	"time"

	"github.com/alterfo/kb/internal/store/vector"
)

func TestComputeRecallAtK(t *testing.T) {
	hits := []vector.ScoredChunk{
		{Chunk: vector.Chunk{RefDocID: "doc-a"}},
		{Chunk: vector.Chunk{RefDocID: "doc-b"}},
		{Chunk: vector.Chunk{RefDocID: "doc-c"}},
	}
	relevant := RelevantSet([]string{"doc-b", "doc-c"})

	if got := ComputeRecallAtK(hits, relevant, 2); got != 0.5 {
		t.Fatalf("recall@2 = %v, want 0.5", got)
	}
	if got := ComputeRecallAtK(hits, relevant, 3); got != 1 {
		t.Fatalf("recall@3 = %v, want 1", got)
	}
	if got := ComputeRecallAtK(hits, relevant, 10); got != 1 {
		t.Fatalf("recall beyond list length = %v, want 1", got)
	}
}

func TestComputeRecallAtKWithoutRelevance(t *testing.T) {
	if got := ComputeRecallAtK(nil, nil, 5); got != 1 {
		t.Fatalf("recall with no relevance labels = %v, want 1", got)
	}
}

func TestEstimateChatCostAccumulates(t *testing.T) {
	first := EstimateChatCost("a", "b")
	if first.PromptTokens == 0 || first.CompletionTokens == 0 || first.TotalTokens != first.PromptTokens+first.CompletionTokens {
		t.Fatalf("EstimateChatCost = %+v", first)
	}

	var total Cost
	total.Add(first)
	total.Add(EstimateChatCost("longer prompt", "completion"))
	if total.PromptTokens <= first.PromptTokens {
		t.Fatalf("expected prompt tokens to accumulate, got %+v", total)
	}
}

func TestLatencyMS(t *testing.T) {
	start := time.Now().Add(-25 * time.Millisecond)
	if got := LatencyMS(start); got < 0 {
		t.Fatalf("LatencyMS = %d, want >= 0", got)
	}
}
