package got

import (
	"context"
	"time"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

type fakeChat struct {
	resp llm.ChatResponse
	err  error
	// calls, when non-nil, is incremented on every Chat call.
	calls *int
}

func (f fakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.resp, f.err
}

// scriptedChat returns responses in order, one per call, cycling to the
// last entry once exhausted; useful when decompose/aggregate/find_gaps/
// synthesize each need a distinct canned response in a single Run.
type scriptedChat struct {
	byPrompt map[string]llm.ChatResponse
	fallback llm.ChatResponse
}

func (s scriptedChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	sys := ""
	if len(req.Messages) > 0 {
		sys = req.Messages[0].Content
	}
	for prefix, resp := range s.byPrompt {
		if len(sys) >= len(prefix) && sys[:len(prefix)] == prefix {
			return resp, nil
		}
	}
	return s.fallback, nil
}

type fakeRetriever struct {
	byQuery map[string][]vector.ScoredChunk
	err     error
	calls   *int
	modes   *[]retriever.Mode
}

func (f fakeRetriever) Retrieve(ctx context.Context, query string, k int) ([]vector.ScoredChunk, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.byQuery[query], nil
}

func (f fakeRetriever) RetrieveMode(ctx context.Context, query string, k int, mode retriever.Mode) ([]vector.ScoredChunk, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.modes != nil {
		*f.modes = append(*f.modes, mode)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.byQuery[query], nil
}

func instantSleep(ctx context.Context, d time.Duration) error { return nil }
