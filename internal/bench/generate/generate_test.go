package generate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/llm"
)

type scriptedChat struct {
	responses []string
	calls     int
	lastReq   llm.ChatRequest
	err       error
}

func (c *scriptedChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	c.calls++
	c.lastReq = req
	if c.err != nil {
		return llm.ChatResponse{}, c.err
	}
	if len(c.responses) == 0 {
		return llm.ChatResponse{}, errors.New("no scripted response")
	}
	idx := c.calls - 1
	if idx >= len(c.responses) {
		idx = len(c.responses) - 1
	}
	return llm.ChatResponse{Content: c.responses[idx], FinishReason: "stop"}, nil
}

func TestGenerateHappyPath(t *testing.T) {
	doc := corpus.Doc{
		ID:         "dsid_ru0000000001",
		SourceType: "doka",
		Title:      "Что такое Docker",
		Body:       "Docker чаще всего применяется для развёртывания серверных приложений.",
	}
	chat := &scriptedChat{responses: []string{
		`{"question":"Для чего чаще всего применяется Docker?","question_type":"single-doc","gold_answer":"для развёртывания серверных приложений","answer_facts":["Docker применяется для развёртывания серверных приложений."]}`,
	}}

	got, err := Generate(context.Background(), chat, "test-model", []corpus.Doc{doc}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("generated = %d, want 1", len(got))
	}
	q := got[0]
	if q.ID != "qgen_"+doc.ID {
		t.Errorf("ID = %q, want %q", q.ID, "qgen_"+doc.ID)
	}
	if q.Type != "single-doc" {
		t.Errorf("Type = %q, want single-doc", q.Type)
	}
	if q.Language != "ru" {
		t.Errorf("Language = %q, want ru", q.Language)
	}
	if len(q.ExpectedDocIDs) != 1 || q.ExpectedDocIDs[0] != doc.ID {
		t.Errorf("ExpectedDocIDs = %v, want [%s]", q.ExpectedDocIDs, doc.ID)
	}
	if len(q.SourceTypes) != 1 || q.SourceTypes[0] != "doka" {
		t.Errorf("SourceTypes = %v, want [doka]", q.SourceTypes)
	}
	if q.Text == "" || q.GoldAnswer == "" {
		t.Errorf("question text/gold answer empty: %+v", q)
	}
}

func TestGenerateDropsHallucinatedGoldAnswer(t *testing.T) {
	doc := corpus.Doc{
		ID:         "dsid_ru0000000002",
		SourceType: "doka",
		Title:      "Тема",
		Body:       "В документе описан Git CLI.",
	}
	chat := &scriptedChat{responses: []string{
		`{"question":"Что такое Docker?","gold_answer":"про несуществующую в документе фразу","answer_facts":["факт"]}`,
	}}

	got, err := Generate(context.Background(), chat, "test-model", []corpus.Doc{doc}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("generated = %d, want 0 (gold answer not in doc body)", len(got))
	}
}

func TestGenerateStripsCodeFence(t *testing.T) {
	doc := corpus.Doc{
		ID:         "dsid_ru0000000003",
		SourceType: "doka",
		Title:      "Nginx",
		Body:       "Nginx разработан Игорем Сысоевым в 2004 году.",
	}
	chat := &scriptedChat{responses: []string{
		"```json\n{\"question\":\"Кем и когда разработан Nginx?\",\"gold_answer\":\"Игорем Сысоевым в 2004 году\",\"answer_facts\":[\"Nginx разработан в 2004 году.\"]}\n```",
	}}

	got, err := Generate(context.Background(), chat, "test-model", []corpus.Doc{doc}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 1 || got[0].GoldAnswer == "" {
		t.Fatalf("generated = %+v, want one parsed question", got)
	}
}

func TestGenerateIncludesSeedExamplesInPrompt(t *testing.T) {
	doc := corpus.Doc{
		ID:         "dsid_ru0000000004",
		SourceType: "doka",
		Title:      "Тема",
		Body:       "Микросервис — это отдельное приложение.",
	}
	seed := []corpus.Question{
		{Text: "Что такое микросервис?", Type: "single-doc", GoldAnswer: "Отдельное небольшое приложение.", AnswerFacts: []string{"факт один"}},
	}
	chat := &scriptedChat{responses: []string{
		`{"question":"Что такое микросервис?","gold_answer":"отдельное приложение","answer_facts":["факт"]}`,
	}}

	if _, err := Generate(context.Background(), chat, "test-model", []corpus.Doc{doc}, seed); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	hay := strings.ToLower(chat.lastReq.Messages[1].Content)
	if !strings.Contains(hay, "примеры") || !strings.Contains(hay, "микросервис") {
		t.Errorf("user prompt missing seed example: %q", chat.lastReq.Messages[1].Content)
	}
}

func TestGenerateReturnsPartialResultOnChatError(t *testing.T) {
	doc := corpus.Doc{
		ID:         "dsid_ru0000000005",
		SourceType: "doka",
		Title:      "Тема",
		Body:       "Текст с фразой из документа.",
	}
	chat := &scriptedChat{err: errors.New("chat down")}

	got, err := Generate(context.Background(), chat, "test-model", []corpus.Doc{doc}, nil)
	if err == nil {
		t.Fatalf("Generate err = nil, want chat error")
	}
	if len(got) != 0 {
		t.Fatalf("generated = %d, want 0 on chat error", len(got))
	}
}
