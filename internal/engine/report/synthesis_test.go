package report

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/guardrails"
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

func chunk(fileName string, score float64) vector.ScoredChunk {
	return vector.ScoredChunk{
		Chunk: vector.Chunk{ID: fileName + "#0", FileName: fileName, FilePath: fileName},
		Score: score,
	}
}

func TestSynthesizeNoChunksFailsOpen(t *testing.T) {
	got := Synthesize(context.Background(), fakeChat{resp: llm.ChatResponse{Content: "should not be used"}}, "m", "q", nil)
	if got != "no information found" {
		t.Fatalf("got %q", got)
	}
}

func TestSynthesizeNilChatFailsOpenToSourceList(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1), chunk("a.md", 0.5)}
	got := Synthesize(context.Background(), nil, "m", "q", chunks)
	want := "relevant sources found but synthesis unavailable: a.md, b.md"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSynthesizeChatErrorFailsOpen(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1)}
	got := Synthesize(context.Background(), fakeChat{err: errors.New("boom")}, "m", "q", chunks)
	if got != "relevant sources found but synthesis unavailable: a.md" {
		t.Fatalf("got %q", got)
	}
}

func TestSynthesizeEmptyReplyFailsOpen(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1)}
	got := Synthesize(context.Background(), fakeChat{resp: llm.ChatResponse{Content: "  "}}, "m", "q", chunks)
	if got != "relevant sources found but synthesis unavailable: a.md" {
		t.Fatalf("got %q", got)
	}
}

func TestSynthesizeUsesChatResponseWithCitation(t *testing.T) {
	got := Synthesize(context.Background(), fakeChat{resp: llm.ChatResponse{Content: "grounded answer (a.md)"}}, "m", "q", []vector.ScoredChunk{chunk("a.md", 1)})
	if got != "grounded answer (a.md)" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildSynthesisPromptMarksSupersededChunk(t *testing.T) {
	chunks := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "c1", FileName: "a.md", Text: "old fact", SupersededBy: "doc-new"}, Score: 1},
		{Chunk: vector.Chunk{ID: "c2", FileName: "b.md", Text: "new fact"}, Score: 1},
	}
	prompt := buildSynthesisPrompt("q", chunks)
	if !strings.Contains(prompt, "(a.md) [superseded] "+guardrails.DataBlock("old fact")) {
		t.Fatalf("expected superseded marker on a.md excerpt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "(b.md) [superseded]") {
		t.Fatalf("active chunk must not be marked superseded, got:\n%s", prompt)
	}
}

type scriptedCall struct {
	resp llm.ChatResponse
	err  error
}

type scriptedChat struct {
	calls    []scriptedCall
	idx      int
	requests []llm.ChatRequest
}

func (s *scriptedChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	s.requests = append(s.requests, req)
	if s.idx >= len(s.calls) {
		return llm.ChatResponse{}, errors.New("unexpected extra chat call")
	}
	c := s.calls[s.idx]
	s.idx++
	return c.resp, c.err
}

func excerptCount(req llm.ChatRequest) int {
	return strings.Count(req.Messages[len(req.Messages)-1].Content, "- (")
}

func TestSynthesizeResultRetrySucceedsWithTrimmedContext(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1), chunk("c.md", 1), chunk("d.md", 1)}
	chat := &scriptedChat{calls: []scriptedCall{
		{err: errors.New("boom")},
		{resp: llm.ChatResponse{Content: "answer (a.md)"}},
	}}
	text, fallback, reason := SynthesizeResult(context.Background(), chat, "m", "q", chunks)
	if fallback {
		t.Fatalf("expected success after retry, got fallback=true reason=%q", reason)
	}
	if text != "answer (a.md)" {
		t.Fatalf("got text %q", text)
	}
	if reason != "" {
		t.Fatalf("expected empty fallback reason on success, got %q", reason)
	}
	if len(chat.requests) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(chat.requests))
	}
	if got := excerptCount(chat.requests[0]); got != 4 {
		t.Fatalf("first attempt should use all 4 chunks, got %d excerpts", got)
	}
	if got := excerptCount(chat.requests[1]); got != 2 {
		t.Fatalf("retry should use trimmed 2 chunks, got %d excerpts", got)
	}
}

func TestSynthesizeResultRetryFallsBackWithReason(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1)}
	chat := &scriptedChat{calls: []scriptedCall{
		{err: errors.New("boom1")},
		{err: errors.New("boom2")},
	}}
	text, fallback, reason := SynthesizeResult(context.Background(), chat, "m", "q", chunks)
	if !fallback {
		t.Fatalf("expected fallback after two failures")
	}
	want := "relevant sources found but synthesis unavailable: a.md, b.md"
	if text != want {
		t.Fatalf("got text %q, want %q", text, want)
	}
	if !strings.Contains(reason, "2 attempt(s)") {
		t.Fatalf("reason should mention attempts, got %q", reason)
	}
	if len(chat.requests) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(chat.requests))
	}
}

func TestSynthesizeResultRetryOnEmptyReply(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1)}
	chat := &scriptedChat{calls: []scriptedCall{
		{resp: llm.ChatResponse{Content: "  "}},
		{resp: llm.ChatResponse{Content: "answer"}},
	}}
	text, fallback, _ := SynthesizeResult(context.Background(), chat, "m", "q", chunks)
	if fallback {
		t.Fatalf("expected success after retry on empty reply")
	}
	if text != "answer" {
		t.Fatalf("got text %q", text)
	}
}

func TestSynthesizeResultSingleChunkDoesNotRetry(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1)}
	chat := &scriptedChat{calls: []scriptedCall{{err: errors.New("boom")}}}
	text, fallback, reason := SynthesizeResult(context.Background(), chat, "m", "q", chunks)
	if !fallback {
		t.Fatalf("expected fallback")
	}
	if text != "relevant sources found but synthesis unavailable: a.md" {
		t.Fatalf("got text %q", text)
	}
	if reason == "" {
		t.Fatalf("expected non-empty fallback reason")
	}
	if len(chat.requests) != 1 {
		t.Fatalf("single chunk must not retry, got %d attempts", len(chat.requests))
	}
}

func TestSynthesizeResultNilChatReason(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1)}
	text, fallback, reason := SynthesizeResult(context.Background(), nil, "m", "q", chunks)
	if !fallback {
		t.Fatalf("expected fallback for nil chat")
	}
	if text != "relevant sources found but synthesis unavailable: a.md" {
		t.Fatalf("got text %q", text)
	}
	if reason == "" {
		t.Fatalf("expected non-empty fallback reason")
	}
}
