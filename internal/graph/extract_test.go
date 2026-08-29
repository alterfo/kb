package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/llm"
)

type fakeChat struct {
	resp  llm.ChatResponse
	err   error
	calls []llm.ChatRequest
}

func (f *fakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.calls = append(f.calls, req)
	return f.resp, f.err
}

func TestExtractChunkValidJSON(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{
		"entities":[{"name":"Alice","type":"person","description":"engineer"}],
		"relations":[{"source":"Alice","target":"Bob","type":"knows","description":"colleagues"}]
	}`}}
	e := NewExtractor(chat, "model")

	got, err := e.ExtractChunk(context.Background(), "Alice knows Bob.")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(got.Entities) != 1 || got.Entities[0].Name != "Alice" {
		t.Fatalf("Entities = %+v", got.Entities)
	}
	if len(got.Relations) != 1 || got.Relations[0].Source != "Alice" {
		t.Fatalf("Relations = %+v", got.Relations)
	}
}

func TestExtractChunkCodeFencedJSON(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: "```json\n{\"entities\":[{\"name\":\"X\",\"type\":\"t\"}],\"relations\":[]}\n```"}}
	e := NewExtractor(chat, "model")

	got, err := e.ExtractChunk(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(got.Entities) != 1 || got.Entities[0].Name != "X" {
		t.Fatalf("Entities = %+v", got.Entities)
	}
}

func TestExtractChunkTrailingCommasTolerated(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{"entities":[{"name":"X","type":"t",},],"relations":[],}`}}
	e := NewExtractor(chat, "model")

	got, err := e.ExtractChunk(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(got.Entities) != 1 || got.Entities[0].Name != "X" {
		t.Fatalf("Entities = %+v", got.Entities)
	}
}

func TestExtractChunkBrokenJSONFailsOpen(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: "not json at all"}}
	e := NewExtractor(chat, "model")

	got, err := e.ExtractChunk(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(got.Entities) != 0 || len(got.Relations) != 0 {
		t.Fatalf("got %+v, want empty extraction", got)
	}
}

func TestExtractChunkTransportErrorFailsOpen(t *testing.T) {
	chat := &fakeChat{err: errors.New("boom")}
	e := NewExtractor(chat, "model")

	got, err := e.ExtractChunk(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(got.Entities) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
}

func TestExtractChunkNilChatFailsOpen(t *testing.T) {
	e := NewExtractor(nil, "model")

	got, err := e.ExtractChunk(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(got.Entities) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
}

func TestExtractChunkEmptyTextSkipsCall(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{"entities":[],"relations":[]}`}}
	e := NewExtractor(chat, "model")

	_, err := e.ExtractChunk(context.Background(), "   ")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(chat.calls) != 0 {
		t.Fatalf("expected no Chat call for empty text, got %d", len(chat.calls))
	}
}

func TestExtractChunkGleaningMergesSecondPass(t *testing.T) {
	calls := 0
	chatFn := chatClientFunc(func(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
		calls++
		if calls == 1 {
			return llm.ChatResponse{Content: `{"entities":[{"name":"A","type":"t"}],"relations":[]}`}, nil
		}
		return llm.ChatResponse{Content: `{"entities":[{"name":"B","type":"t"}],"relations":[]}`}, nil
	})

	e := &Extractor{Chat: chatFn, Model: "model", Gleaning: true}
	got, err := e.ExtractChunk(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(got.Entities) != 2 {
		t.Fatalf("got %+v, want 2 entities from both passes", got.Entities)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestExtractChunkGleaningDisabledSkipsSecondCall(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{"entities":[{"name":"A","type":"t"}],"relations":[]}`}}
	e := NewExtractor(chat, "model")

	_, err := e.ExtractChunk(context.Background(), "text")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(chat.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (gleaning disabled)", len(chat.calls))
	}
}

type chatClientFunc func(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)

func (f chatClientFunc) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return f(ctx, req)
}
