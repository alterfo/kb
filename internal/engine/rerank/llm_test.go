package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

type fakeChat struct {
	resp llm.ChatResponse
	err  error
}

func (f fakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return f.resp, f.err
}

func candidates(ids ...string) []vector.ScoredChunk {
	out := make([]vector.ScoredChunk, len(ids))
	for i, id := range ids {
		out[i] = vector.ScoredChunk{Chunk: vector.Chunk{ID: id, Text: id}}
	}
	return out
}

func TestLLMRerankReordersByFakeLLM(t *testing.T) {
	cands := candidates("a", "b", "c")
	l := NewLLM(fakeChat{resp: llm.ChatResponse{Content: `[2, 0, 1]`}}, "model")

	got, err := l.Rerank(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	want := []string{"c", "a", "b"}
	for i, id := range want {
		if got[i].Chunk.ID != id {
			t.Fatalf("got order %v, want %v", ids(got), want)
		}
	}
}

func TestLLMRerankMalformedJSONFailsOpen(t *testing.T) {
	cands := candidates("a", "b", "c")
	l := NewLLM(fakeChat{resp: llm.ChatResponse{Content: "not json"}}, "model")

	got, err := l.Rerank(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if !sameOrder(got, cands) {
		t.Fatalf("got %v, want original order %v", ids(got), ids(cands))
	}
}

func TestLLMRerankNotAPermutationFailsOpen(t *testing.T) {
	cands := candidates("a", "b", "c")
	l := NewLLM(fakeChat{resp: llm.ChatResponse{Content: `[0, 0, 1]`}}, "model")

	got, err := l.Rerank(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if !sameOrder(got, cands) {
		t.Fatalf("got %v, want original order %v", ids(got), ids(cands))
	}
}

func TestLLMRerankWrongLengthFailsOpen(t *testing.T) {
	cands := candidates("a", "b", "c")
	l := NewLLM(fakeChat{resp: llm.ChatResponse{Content: `[0, 1]`}}, "model")

	got, err := l.Rerank(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if !sameOrder(got, cands) {
		t.Fatalf("got %v, want original order %v", ids(got), ids(cands))
	}
}

func TestLLMRerankTimeoutFailsOpen(t *testing.T) {
	cands := candidates("a", "b", "c")
	l := NewLLM(fakeChat{err: errors.New("context deadline exceeded")}, "model")

	got, err := l.Rerank(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if !sameOrder(got, cands) {
		t.Fatalf("got %v, want original order %v", ids(got), ids(cands))
	}
}

func TestLLMRerankNilChatFailsOpen(t *testing.T) {
	cands := candidates("a", "b")
	l := NewLLM(nil, "model")

	got, err := l.Rerank(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if !sameOrder(got, cands) {
		t.Fatalf("got %v, want original order %v", ids(got), ids(cands))
	}
}

func TestLLMRerankEmptyCandidates(t *testing.T) {
	l := NewLLM(fakeChat{resp: llm.ChatResponse{Content: `[]`}}, "model")

	got, err := l.Rerank(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestLLMRerankParsesCodeFencedJSON(t *testing.T) {
	cands := candidates("a", "b")
	l := NewLLM(fakeChat{resp: llm.ChatResponse{Content: "```json\n[1, 0]\n```"}}, "model")

	got, err := l.Rerank(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if got[0].Chunk.ID != "b" || got[1].Chunk.ID != "a" {
		t.Fatalf("got %v, want [b a]", ids(got))
	}
}

func TestLLMRerankBeyondListwiseCapKeepsTailOrder(t *testing.T) {
	ids := make([]string, 45)
	for i := range ids {
		ids[i] = string(rune('a' + i))
	}
	cands := candidates(ids...)

	l := NewLLM(fakeChat{resp: llm.ChatResponse{Content: reversedIndicesJSON(maxListwise)}}, "model")
	got, err := l.Rerank(context.Background(), "q", cands)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != len(cands) {
		t.Fatalf("got %d candidates, want %d", len(got), len(cands))
	}
	if got[0].Chunk.ID != cands[maxListwise-1].Chunk.ID {
		t.Fatalf("expected reversed head, got first=%s", got[0].Chunk.ID)
	}
	for i := maxListwise; i < len(cands); i++ {
		if got[i].Chunk.ID != cands[i].Chunk.ID {
			t.Fatalf("expected tail beyond cap to keep original order at %d: got %s want %s", i, got[i].Chunk.ID, cands[i].Chunk.ID)
		}
	}
}

func reversedIndicesJSON(n int) string {
	order := make([]int, n)
	for i := range order {
		order[i] = n - 1 - i
	}
	b, _ := json.Marshal(order)
	return string(b)
}

func ids(cands []vector.ScoredChunk) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Chunk.ID
	}
	return out
}

func sameOrder(a, b []vector.ScoredChunk) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Chunk.ID != b[i].Chunk.ID {
			return false
		}
	}
	return true
}
