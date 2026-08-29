package got

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestSynthesizeNoChunksFailsOpen(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: "should not be used"}}})
	got := o.synthesize(context.Background(), "q", nil, nil, nil)
	if got != "no information found" {
		t.Fatalf("got %q", got)
	}
}

func TestSynthesizeNilChatFailsOpenToSourceList(t *testing.T) {
	o := New(Config{})
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1), chunk("a.md", 0.5)}
	got := o.synthesize(context.Background(), "q", chunks, nil, nil)
	want := "relevant sources found but synthesis unavailable: a.md, b.md"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSynthesizeChatErrorFailsOpen(t *testing.T) {
	o := New(Config{Chat: fakeChat{err: errors.New("boom")}})
	chunks := []vector.ScoredChunk{chunk("a.md", 1)}
	got := o.synthesize(context.Background(), "q", chunks, nil, nil)
	if got != "relevant sources found but synthesis unavailable: a.md" {
		t.Fatalf("got %q", got)
	}
}

func TestSynthesizeUsesChatResponse(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: "grounded answer (a.md)"}}})
	chunks := []vector.ScoredChunk{chunk("a.md", 1)}
	got := o.synthesize(context.Background(), "q", chunks, nil, nil)
	if got != "grounded answer (a.md)" {
		t.Fatalf("got %q", got)
	}
}

func TestSynthesizeRejectsLeakedToolCallSyntax(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1)}
	chat := &synthesisScriptedChat{calls: []synthesisScriptedCall{
		{resp: llm.ChatResponse{Content: `<function_calls><invoke name="Bash">git status</invoke></function_calls>`}},
		{resp: llm.ChatResponse{Content: "grounded answer (a.md)"}},
	}}
	o := New(Config{Chat: chat})

	text, fallback, _ := o.synthesizeResult(context.Background(), "q", chunks, nil, nil)
	if fallback {
		t.Fatalf("expected success after retry past leaked tool-call syntax")
	}
	if text != "grounded answer (a.md)" {
		t.Fatalf("got text %q", text)
	}
	if len(chat.requests) != 2 {
		t.Fatalf("expected retry after degenerate reply, got %d attempts", len(chat.requests))
	}
}

func TestSynthesizeRejectsGenericChatFiller(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1)}
	chat := &synthesisScriptedChat{calls: []synthesisScriptedCall{
		{resp: llm.ChatResponse{Content: "Hi there! How can I help you today?"}},
	}}
	o := New(Config{Chat: chat})

	text, fallback, reason := o.synthesizeResult(context.Background(), "q", chunks, nil, nil)
	if !fallback {
		t.Fatalf("expected fallback for generic chat filler, got text %q", text)
	}
	if text != "relevant sources found but synthesis unavailable: a.md" {
		t.Fatalf("got text %q", text)
	}
	if reason == "" {
		t.Fatalf("expected non-empty fallback reason")
	}
}

func TestAggregateRejectsLeakedToolCallSyntax(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: `<invoke name="Bash">git log</invoke>`}}})
	results := []subgoalResult{{Query: "sub1", Answer: "answer1"}}
	got := o.aggregate(context.Background(), "q", results)
	if got != "sub1\nanswer1" {
		t.Fatalf("expected fallback concatenation, got %q", got)
	}
}

func TestFallbackAggregateDropsPlaceholderAnswers(t *testing.T) {
	results := []subgoalResult{
		{Query: "sub1", Answer: "relevant sources found but synthesis unavailable: a.md"},
		{Query: "sub2", Answer: "real grounded answer (b.md)"},
	}
	got := fallbackAggregate(results)
	if got != "sub2\nreal grounded answer (b.md)" {
		t.Fatalf("got %q", got)
	}
}

func TestFallbackAggregateKeepsPlaceholderWhenNoRealAnswer(t *testing.T) {
	results := []subgoalResult{{Query: "sub1", Answer: "no information found"}}
	got := fallbackAggregate(results)
	if got != "no information found" {
		t.Fatalf("got %q, want fail-open placeholder preserved", got)
	}
}

func TestAggregateNilChatFailsOpenToConcatenation(t *testing.T) {
	o := New(Config{})
	results := []subgoalResult{
		{Query: "sub1", Answer: "answer1"},
		{Query: "sub2", Answer: "answer2"},
	}
	got := o.aggregate(context.Background(), "q", results)
	want := "sub1\nanswer1\n\nsub2\nanswer2"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAggregateChatErrorFailsOpen(t *testing.T) {
	o := New(Config{Chat: fakeChat{err: errors.New("boom")}})
	results := []subgoalResult{{Query: "sub1", Answer: "answer1"}}
	got := o.aggregate(context.Background(), "q", results)
	if got != "sub1\nanswer1" {
		t.Fatalf("got %q", got)
	}
}

func TestAggregateUsesChatResponse(t *testing.T) {
	o := New(Config{Chat: fakeChat{resp: llm.ChatResponse{Content: "combined answer"}}})
	results := []subgoalResult{{Query: "sub1", Answer: "answer1"}}
	got := o.aggregate(context.Background(), "q", results)
	if got != "combined answer" {
		t.Fatalf("got %q", got)
	}
}

func TestSourcesFromChunks(t *testing.T) {
	chunks := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "c1", FileName: "a.md", FilePath: "notes/a.md"}, Score: 1},
	}
	got := sourcesFromChunks(chunks)
	if len(got) != 1 || got[0].ChunkID != "c1" || got[0].FilePath != "notes/a.md" {
		t.Fatalf("got %+v", got)
	}
}

func TestSourcesFromChunksUsesRawDocID(t *testing.T) {
	chunks := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "c1", RefDocID: "wiki/dsid_ru0001", Metadata: map[string]string{"id": "dsid_ru0001"}}, Score: 1},
	}
	got := sourcesFromChunks(chunks)
	if len(got) != 1 || got[0].DocID != "dsid_ru0001" {
		t.Fatalf("got %+v, want raw document id dsid_ru0001", got)
	}
}

func TestSourcesFromChunksCarriesSupersededBy(t *testing.T) {
	chunks := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "c1", FileName: "a.md", FilePath: "notes/a.md", SupersededBy: "doc-new"}, Score: 1},
	}
	got := sourcesFromChunks(chunks)
	if len(got) != 1 || got[0].SupersededBy != "doc-new" {
		t.Fatalf("got %+v", got)
	}
}

func TestBuildSynthesizePromptMarksSupersededChunk(t *testing.T) {
	chunks := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "c1", FileName: "a.md", Text: "old fact", SupersededBy: "doc-new"}, Score: 1},
		{Chunk: vector.Chunk{ID: "c2", FileName: "b.md", Text: "new fact"}, Score: 1},
	}
	prompt := buildSynthesizePrompt("q", chunks, nil, nil)
	if !strings.Contains(prompt, "(a.md) [superseded] old fact") {
		t.Fatalf("expected superseded marker on a.md excerpt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "(b.md) [superseded]") {
		t.Fatalf("active chunk must not be marked superseded, got:\n%s", prompt)
	}
}

type synthesisScriptedCall struct {
	resp llm.ChatResponse
	err  error
}

type synthesisScriptedChat struct {
	calls    []synthesisScriptedCall
	idx      int
	requests []llm.ChatRequest
}

func (s *synthesisScriptedChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	s.requests = append(s.requests, req)
	if s.idx >= len(s.calls) {
		return llm.ChatResponse{}, errors.New("unexpected extra chat call")
	}
	c := s.calls[s.idx]
	s.idx++
	return c.resp, c.err
}

func synthesizeExcerptCount(req llm.ChatRequest) int {
	return strings.Count(req.Messages[len(req.Messages)-1].Content, "- (")
}

func TestSynthesizeResultRetrySucceedsWithTrimmedContext(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1), chunk("c.md", 1), chunk("d.md", 1)}
	chat := &synthesisScriptedChat{calls: []synthesisScriptedCall{
		{err: errors.New("boom")},
		{resp: llm.ChatResponse{Content: "answer (a.md)"}},
	}}
	o := New(Config{Chat: chat})

	text, fallback, reason := o.synthesizeResult(context.Background(), "q", chunks, nil, nil)
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
	if got := synthesizeExcerptCount(chat.requests[0]); got != 4 {
		t.Fatalf("first attempt should use all 4 chunks, got %d excerpts", got)
	}
	if got := synthesizeExcerptCount(chat.requests[1]); got != 2 {
		t.Fatalf("retry should use trimmed 2 chunks, got %d excerpts", got)
	}
}

func TestSynthesizeResultRetryFallsBackWithReason(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1), chunk("b.md", 1)}
	chat := &synthesisScriptedChat{calls: []synthesisScriptedCall{
		{err: errors.New("boom1")},
		{err: errors.New("boom2")},
	}}
	o := New(Config{Chat: chat})

	text, fallback, reason := o.synthesizeResult(context.Background(), "q", chunks, nil, nil)
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
	chat := &synthesisScriptedChat{calls: []synthesisScriptedCall{
		{resp: llm.ChatResponse{Content: "  "}},
		{resp: llm.ChatResponse{Content: "answer"}},
	}}
	o := New(Config{Chat: chat})

	text, fallback, _ := o.synthesizeResult(context.Background(), "q", chunks, nil, nil)
	if fallback {
		t.Fatalf("expected success after retry on empty reply")
	}
	if text != "answer" {
		t.Fatalf("got text %q", text)
	}
}

func TestSynthesizeResultSingleChunkDoesNotRetry(t *testing.T) {
	chunks := []vector.ScoredChunk{chunk("a.md", 1)}
	chat := &synthesisScriptedChat{calls: []synthesisScriptedCall{{err: errors.New("boom")}}}
	o := New(Config{Chat: chat})

	text, fallback, reason := o.synthesizeResult(context.Background(), "q", chunks, nil, nil)
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
	o := New(Config{})

	text, fallback, reason := o.synthesizeResult(context.Background(), "q", chunks, nil, nil)
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
