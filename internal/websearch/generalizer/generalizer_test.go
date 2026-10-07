package generalizer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/websearch/guard"
)

type fakeChat struct {
	fn    func(call int, req llm.ChatRequest) (llm.ChatResponse, error)
	calls int
}

func (f *fakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.calls++
	return f.fn(f.calls, req)
}

func newTestGeneralizer() *Generalizer {
	return New(guard.New(guard.Options{}), "test-model")
}

func TestProposeReturnsSafeQuery(t *testing.T) {
	chat := &fakeChat{fn: func(_ int, _ llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{Content: "How does SQLite FTS5 tokenization work?"}, nil
	}}
	got, err := newTestGeneralizer().Propose(context.Background(), chat, "как работает FTS5?", "")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if got != "How does SQLite FTS5 tokenization work?" {
		t.Fatalf("query = %q", got)
	}
	if chat.calls != 1 {
		t.Fatalf("chat calls = %d, want 1", chat.calls)
	}
}

func TestProposeStripsThink(t *testing.T) {
	chat := &fakeChat{fn: func(_ int, _ llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{Content: "<think>the user means alice@example.com internal project</think>general SQLite FTS5 tokenization"}, nil
	}}
	got, err := newTestGeneralizer().Propose(context.Background(), chat, "question", "")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if got != "general SQLite FTS5 tokenization" {
		t.Fatalf("query = %q", got)
	}
}

func TestProposeRetriesThenRefuses(t *testing.T) {
	var calls []llm.ChatRequest
	chat := &fakeChat{fn: func(call int, req llm.ChatRequest) (llm.ChatResponse, error) {
		calls = append(calls, req)
		if call == 1 {
			return llm.ChatResponse{Content: "details for alice@example.com"}, nil
		}
		return llm.ChatResponse{Content: "still alice@example.com"}, nil
	}}
	_, err := newTestGeneralizer().Propose(context.Background(), chat, "question", "")
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want *BlockedError", err)
	}
	if !hasReason(blocked.Reasons, guard.ReasonEmail) {
		t.Fatalf("reasons = %v, want email", blocked.Reasons)
	}
	if chat.calls != 2 {
		t.Fatalf("chat calls = %d, want 2", chat.calls)
	}
	if len(calls) != 2 {
		t.Fatalf("captured calls = %d, want 2", len(calls))
	}
	last := calls[1].Messages[len(calls[1].Messages)-1]
	if !strings.Contains(last.Content, "email") {
		t.Fatalf("retry instruction missing reason, got %q", last.Content)
	}
}

func TestProposeRetriesThenSucceeds(t *testing.T) {
	chat := &fakeChat{fn: func(call int, _ llm.ChatRequest) (llm.ChatResponse, error) {
		if call == 1 {
			return llm.ChatResponse{Content: "alice@example.com contact details"}, nil
		}
		return llm.ChatResponse{Content: "SQLite FTS5 tokenization"}, nil
	}}
	got, err := newTestGeneralizer().Propose(context.Background(), chat, "question", "")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if got != "SQLite FTS5 tokenization" {
		t.Fatalf("query = %q", got)
	}
	if chat.calls != 2 {
		t.Fatalf("chat calls = %d, want 2", chat.calls)
	}
}

func TestProposeEmptyAnswer(t *testing.T) {
	chat := &fakeChat{fn: func(_ int, _ llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{Content: "<think>reasoning only</think>"}, nil
	}}
	_, err := newTestGeneralizer().Propose(context.Background(), chat, "question", "")
	if !errors.Is(err, ErrEmpty) {
		t.Fatalf("err = %v, want ErrEmpty", err)
	}
	if chat.calls != 1 {
		t.Fatalf("chat calls = %d, want 1", chat.calls)
	}
}

func TestProposeLLMError(t *testing.T) {
	chat := &fakeChat{fn: func(_ int, _ llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{}, errors.New("llm down")
	}}
	_, err := newTestGeneralizer().Propose(context.Background(), chat, "question", "")
	if err == nil {
		t.Fatal("want error from LLM, got nil")
	}
	if !strings.Contains(err.Error(), "llm down") {
		t.Fatalf("err = %v, want wrapped LLM error", err)
	}
}

func TestProposePromptContainsQuestionAndContext(t *testing.T) {
	var req llm.ChatRequest
	chat := &fakeChat{fn: func(_ int, r llm.ChatRequest) (llm.ChatResponse, error) {
		req = r
		return llm.ChatResponse{Content: "safe general query"}, nil
	}}
	_, err := newTestGeneralizer().Propose(context.Background(), chat, "who is alice", "answer mentions alice")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != "system" {
		t.Fatalf("first role = %q, want system", req.Messages[0].Role)
	}
	if !strings.Contains(req.Messages[1].Content, "who is alice") {
		t.Fatalf("prompt missing question: %q", req.Messages[1].Content)
	}
	if !strings.Contains(req.Messages[1].Content, "answer mentions alice") {
		t.Fatalf("prompt missing context: %q", req.Messages[1].Content)
	}
	if !req.Messages[1].Untrusted {
		t.Fatal("user message must be marked untrusted")
	}
}

func TestProposeNilGuard(t *testing.T) {
	g := &Generalizer{Model: "m"}
	_, err := g.Propose(context.Background(), &fakeChat{}, "question", "")
	if err == nil {
		t.Fatal("want error for nil guard")
	}
}

func hasReason(reasons []guard.Reason, want guard.Reason) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}
