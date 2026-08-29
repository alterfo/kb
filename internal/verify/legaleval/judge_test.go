package legaleval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/llm"
)

type chatFunc func(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)

func (f chatFunc) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return f(ctx, req)
}

func TestStatuteRelevancePrompt(t *testing.T) {
	p := statuteRelevancePrompt("Что такое злоупотребление правом?", Article{ID: "к/ст10", Number: "10", Title: "Злоупотребление правом", Body: "Текст статьи."})
	for _, want := range []string{"Что такое злоупотребление правом?", "к/ст10", "Злоупотребление правом", "Текст статьи."} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}

func TestClaimTruthfulnessPrompt(t *testing.T) {
	p := claimTruthfulnessPrompt("Q", "A", []string{"ev1", "ev2"})
	for _, want := range []string{"Q", "A", "ev1", "ev2"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
	if p2 := claimTruthfulnessPrompt("Q", "A", nil); !strings.Contains(p2, "Evidence: none") {
		t.Errorf("empty evidence prompt = %q", p2)
	}
}

func TestParseVerdict(t *testing.T) {
	v, err := parseVerdict(`{"passed": true, "reason": "ok"}`)
	if err != nil {
		t.Fatalf("parseVerdict: %v", err)
	}
	if !v.Passed || v.Detail != "ok" {
		t.Fatalf("verdict = %+v", v)
	}
	v, err = parseVerdict("```json\n{\"passed\": false}\n```")
	if err != nil {
		t.Fatalf("parseVerdict fenced: %v", err)
	}
	if v.Passed {
		t.Fatal("fenced verdict must be not passed")
	}
	if _, err := parseVerdict("not json"); err == nil {
		t.Fatal("expected error for invalid verdict")
	}
}

func TestLLMJudgeStatuteRelevant(t *testing.T) {
	j := &LLMJudge{Chat: chatFunc(func(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
		if len(req.Messages) < 2 || req.Messages[0].Role != "system" || !strings.Contains(req.Messages[1].Content, "к/ст1") {
			t.Fatalf("unexpected request: %+v", req)
		}
		return llm.ChatResponse{Content: `{"passed": true, "reason": "r"}`}, nil
	}), Model: "m"}
	v, err := j.StatuteRelevant(context.Background(), "Q", Article{ID: "к/ст1", Body: "b"})
	if err != nil {
		t.Fatalf("StatuteRelevant: %v", err)
	}
	if !v.Passed {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestLLMJudgeStatuteRelevantChatError(t *testing.T) {
	j := &LLMJudge{Chat: chatFunc(func(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{}, errors.New("chat down")
	}), Model: "m"}
	if _, err := j.StatuteRelevant(context.Background(), "Q", Article{ID: "к/ст1"}); err == nil {
		t.Fatal("expected chat error")
	}
}

func TestLLMJudgeClaimTruthfulInvalidJSON(t *testing.T) {
	j := &LLMJudge{Chat: chatFunc(func(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{Content: "bogus"}, nil
	}), Model: "m"}
	if _, err := j.ClaimTruthful(context.Background(), "Q", "A", nil); err == nil {
		t.Fatal("expected verdict parse error")
	}
}

func TestLLMJudgeNilChat(t *testing.T) {
	j := &LLMJudge{}
	if _, err := j.StatuteRelevant(context.Background(), "Q", Article{}); err == nil {
		t.Fatal("expected error for nil chat")
	}
	if _, err := j.ClaimTruthful(context.Background(), "Q", "A", nil); err == nil {
		t.Fatal("expected error for nil chat")
	}
}
