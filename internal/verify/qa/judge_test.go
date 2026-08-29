package qa

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/llm"
)

type judgeFakeChat struct {
	fn func(req llm.ChatRequest) (llm.ChatResponse, error)
}

func (f judgeFakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if f.fn == nil {
		return llm.ChatResponse{}, errors.New("no response")
	}
	return f.fn(req)
}

func TestLLMJudge_UsesModelVerdict(t *testing.T) {
	judge := NewLLMJudge(judgeFakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{Content: `{"passed": true, "reason": "matches"}`}, nil
	}}, "test-model")

	v, err := judge.Judge(context.Background(), "q", "a", "a")
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !v.Passed || v.Reason != "matches" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestLLMJudge_ChatErrorReturnsError(t *testing.T) {
	judge := NewLLMJudge(judgeFakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{}, errors.New("boom")
	}}, "test-model")

	_, err := judge.Judge(context.Background(), "q", "a", "a")
	if err == nil {
		t.Fatal("Judge chat error should be returned, got nil")
	}
}

func TestLLMJudge_ParseErrorReturnsError(t *testing.T) {
	judge := NewLLMJudge(judgeFakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{Content: `not json`}, nil
	}}, "test-model")

	_, err := judge.Judge(context.Background(), "q", "a", "a")
	if err == nil {
		t.Fatal("Judge parse error should be returned, got nil")
	}
}

func TestLLMJudge_NilChatReturnsError(t *testing.T) {
	judge := NewLLMJudge(nil, "test-model")
	_, err := judge.Judge(context.Background(), "q", "a", "a")
	if err == nil {
		t.Fatal("Judge nil chat should be returned as an error, got nil")
	}
}

func TestOverlap_Jaccard(t *testing.T) {
	if got := Overlap("a b c", "a b d"); got == 0 {
		t.Fatalf("Overlap = %v, want > 0", got)
	}
	if got := Overlap("x y", "p q"); got != 0 {
		t.Fatalf("Overlap = %v, want 0", got)
	}
	if got := Overlap("", "a"); got != 0 {
		t.Fatalf("Overlap(empty) = %v, want 0", got)
	}
}
