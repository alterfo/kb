package verify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/llm"
)

type fakeChatClient struct {
	resp llm.ChatResponse
	err  error
}

func (f fakeChatClient) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return f.resp, f.err
}

func contradictionChunks() []Chunk {
	return []Chunk{
		{ChunkID: "c1", FileName: "a.md", Text: "Article 5 as amended in 2015 allows X."},
		{ChunkID: "c2", FileName: "b.md", Text: "Article 5 in the current redaction forbids X."},
	}
}

func TestContradictionDetectorFlagsContradiction(t *testing.T) {
	chat := fakeChatClient{resp: llm.ChatResponse{Content: `[{"chunk_a":"c1","chunk_b":"c2","reason":"conflicting provisions"}]`}}
	rep, err := NewContradictionDetector(chat, "").Detect(context.Background(), "article 5", contradictionChunks())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.HasContradictions() || len(rep.Contradictions) != 1 {
		t.Fatalf("got %+v, want one contradiction", rep)
	}
	c := rep.Contradictions[0]
	if c.ChunkA != "c1" || c.ChunkB != "c2" || c.Reason != "conflicting provisions" {
		t.Fatalf("got %+v", c)
	}
}

func TestContradictionDetectorNoContradiction(t *testing.T) {
	chat := fakeChatClient{resp: llm.ChatResponse{Content: `[]`}}
	rep, err := NewContradictionDetector(chat, "").Detect(context.Background(), "q", contradictionChunks())
	if err != nil || rep.HasContradictions() {
		t.Fatalf("got %+v, err %v", rep, err)
	}
}

func TestContradictionDetectorInvalidJSONFailsOpen(t *testing.T) {
	chat := fakeChatClient{resp: llm.ChatResponse{Content: "not json"}}
	rep, err := NewContradictionDetector(chat, "").Detect(context.Background(), "q", contradictionChunks())
	if err == nil {
		t.Fatal("expected parse error")
	}
	if rep.HasContradictions() {
		t.Fatalf("got %+v, want empty report on fail-open", rep)
	}
}

func TestContradictionDetectorChatErrorFailsOpen(t *testing.T) {
	chat := fakeChatClient{err: errors.New("boom")}
	rep, err := NewContradictionDetector(chat, "").Detect(context.Background(), "q", contradictionChunks())
	if err == nil {
		t.Fatal("expected chat error")
	}
	if rep.HasContradictions() {
		t.Fatalf("got %+v, want empty report on fail-open", rep)
	}
}

func TestContradictionDetectorNilChatFailsOpen(t *testing.T) {
	rep, err := NewContradictionDetector(nil, "").Detect(context.Background(), "q", contradictionChunks())
	if err != nil || rep.HasContradictions() {
		t.Fatalf("got %+v, err %v", rep, err)
	}
}

func TestContradictionDetectorSkipsSingleChunk(t *testing.T) {
	chat := fakeChatClient{resp: llm.ChatResponse{Content: `[{"chunk_a":"c1","chunk_b":"c2","reason":"x"}]`}}
	rep, err := NewContradictionDetector(chat, "").Detect(context.Background(), "q", []Chunk{{ChunkID: "c1", Text: "only one"}})
	if err != nil || rep.HasContradictions() {
		t.Fatalf("single chunk must skip detection, got %+v err %v", rep, err)
	}
}

func TestContradictionDetectorFenceAndEmptyFilter(t *testing.T) {
	chat := fakeChatClient{resp: llm.ChatResponse{Content: "```json\n[{\"chunk_a\":\"c1\",\"chunk_b\":\"\",\"reason\":\"  \"},{\"chunk_a\":\"\",\"chunk_b\":\"\",\"reason\":\"x\"}]\n```"}}
	rep, err := NewContradictionDetector(chat, "").Detect(context.Background(), "q", contradictionChunks())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Contradictions) != 1 {
		t.Fatalf("got %+v, want one kept entry", rep.Contradictions)
	}
	if rep.Contradictions[0].ChunkA != "c1" || rep.Contradictions[0].Reason != "" {
		t.Fatalf("got %+v", rep.Contradictions[0])
	}
}

func TestBuildContradictionPrompt(t *testing.T) {
	p := buildContradictionPrompt("article 5", contradictionChunks())
	if !strings.Contains(p, "Question: article 5") ||
		!strings.Contains(p, "[c1]") || !strings.Contains(p, "allows X") ||
		!strings.Contains(p, "[c2]") {
		t.Fatalf("prompt missing labels or texts: %s", p)
	}
}

func TestBuildContradictionPromptFileNameLabel(t *testing.T) {
	p := buildContradictionPrompt("", []Chunk{{FileName: "a.md", Text: "text"}})
	if !strings.Contains(p, "[a.md]") {
		t.Fatalf("prompt missing file-name label: %s", p)
	}
}
