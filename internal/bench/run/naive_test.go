package run

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

type naiveFakeRetriever struct {
	chunks []vector.ScoredChunk
	err    error
	gotK   int
	calls  int
}

func (f *naiveFakeRetriever) Retrieve(ctx context.Context, query string, opt retriever.Options) ([]vector.ScoredChunk, error) {
	f.calls++
	f.gotK = opt.K
	if f.err != nil {
		return nil, f.err
	}
	return f.chunks, nil
}

type naiveFakeChat struct {
	resp  llm.ChatResponse
	err   error
	calls int
	reqs  []llm.ChatRequest
}

func (f *naiveFakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.calls++
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return llm.ChatResponse{}, f.err
	}
	return f.resp, nil
}

func TestNaiveAnswerNormal(t *testing.T) {
	r := &naiveFakeRetriever{chunks: []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "c1", RefDocID: "42", Text: " first excerpt ", FileName: "a.md"}},
		{Chunk: vector.Chunk{ID: "c2", RefDocID: "42", Text: "second excerpt", FileName: "b.md"}},
		{Chunk: vector.Chunk{ID: "c3", RefDocID: "7", Text: "third excerpt"}},
	}}
	chat := &naiveFakeChat{resp: llm.ChatResponse{Content: "  the answer  ", FinishReason: "stop"}}

	answer, docIDs, err := NaiveAnswer(context.Background(), r, chat, "test-model", 5, "what is kb")
	if err != nil {
		t.Fatalf("NaiveAnswer: %v", err)
	}
	if answer != "the answer" {
		t.Errorf("answer = %q, want %q", answer, "the answer")
	}
	if len(docIDs) != 2 || docIDs[0] != "42" || docIDs[1] != "7" {
		t.Errorf("docIDs = %v, want [42 7]", docIDs)
	}
	if r.calls != 1 {
		t.Errorf("retriever calls = %d, want 1", r.calls)
	}
	if r.gotK != 5 {
		t.Errorf("retriever k = %d, want 5", r.gotK)
	}
	if chat.calls != 1 {
		t.Fatalf("chat calls = %d, want 1", chat.calls)
	}
	req := chat.reqs[0]
	if req.Model != "test-model" {
		t.Errorf("model = %q, want test-model", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(req.Messages))
	}
	hay := strings.Join([]string{req.Messages[0].Content, req.Messages[1].Content}, "\n")
	if !strings.Contains(hay, "what is kb") {
		t.Errorf("prompt missing query: %q", hay)
	}
	if !strings.Contains(hay, "first excerpt") || !strings.Contains(hay, "doc 42") {
		t.Errorf("prompt missing source excerpts: %q", hay)
	}
}

func TestNaiveAnswerUsesRawDocID(t *testing.T) {
	r := &naiveFakeRetriever{chunks: []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "c1", RefDocID: "dragon/4", Metadata: map[string]string{"id": "4"}, Text: "excerpt one"}},
		{Chunk: vector.Chunk{ID: "c2", RefDocID: "dragon/4", Metadata: map[string]string{"id": "4"}, Text: "excerpt two"}},
		{Chunk: vector.Chunk{ID: "c3", RefDocID: "dragon/7", Metadata: map[string]string{"id": "7"}, Text: "excerpt three"}},
	}}
	chat := &naiveFakeChat{resp: llm.ChatResponse{Content: "answer", FinishReason: "stop"}}

	_, docIDs, err := NaiveAnswer(context.Background(), r, chat, "test-model", 3, "question")
	if err != nil {
		t.Fatalf("NaiveAnswer: %v", err)
	}
	if len(docIDs) != 2 || docIDs[0] != "4" || docIDs[1] != "7" {
		t.Errorf("docIDs = %v, want [4 7]", docIDs)
	}
}

func TestNaiveAnswerEmptyRetrieval(t *testing.T) {
	r := &naiveFakeRetriever{}
	chat := &naiveFakeChat{resp: llm.ChatResponse{Content: "no answer", FinishReason: "stop"}}

	answer, docIDs, err := NaiveAnswer(context.Background(), r, chat, "test-model", 3, "question")
	if err != nil {
		t.Fatalf("NaiveAnswer: %v", err)
	}
	if answer != "no answer" {
		t.Errorf("answer = %q, want %q", answer, "no answer")
	}
	if len(docIDs) != 0 {
		t.Errorf("docIDs = %v, want empty", docIDs)
	}
	if chat.calls != 1 {
		t.Errorf("chat calls = %d, want 1", chat.calls)
	}
	hay := chat.reqs[0].Messages[1].Content
	if !strings.Contains(hay, "no source excerpts retrieved") {
		t.Errorf("prompt missing empty-sources marker: %q", hay)
	}
}

func TestNaiveAnswerChatError(t *testing.T) {
	r := &naiveFakeRetriever{chunks: []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "c1", RefDocID: "42", Text: "excerpt"}},
	}}
	wantErr := errors.New("chat boom")
	chat := &naiveFakeChat{err: wantErr}

	_, _, err := NaiveAnswer(context.Background(), r, chat, "test-model", 3, "question")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if chat.calls != 1 {
		t.Errorf("chat calls = %d, want 1", chat.calls)
	}
}

func TestNaiveAnswerRetrieveError(t *testing.T) {
	wantErr := errors.New("retrieve boom")
	r := &naiveFakeRetriever{err: wantErr}
	chat := &naiveFakeChat{resp: llm.ChatResponse{Content: "answer"}}

	_, _, err := NaiveAnswer(context.Background(), r, chat, "test-model", 3, "question")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if chat.calls != 0 {
		t.Errorf("chat calls = %d, want 0", chat.calls)
	}
}
