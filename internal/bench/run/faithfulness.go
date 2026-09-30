package run

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/llm"
)

type FaithfulnessJudge interface {
	Judge(ctx context.Context, answer string, chunks []ContextChunk) (float64, error)
}

type LLMFaithfulnessJudge struct {
	chat  ChatClient
	model string
}

func NewLLMFaithfulnessJudge(chat ChatClient, model string) *LLMFaithfulnessJudge {
	return &LLMFaithfulnessJudge{chat: chat, model: model}
}

const faithfulnessSystemPrompt = "You are a strict evaluator. Given an ANSWER and the CONTEXT passages used to produce it, decide whether every factual claim in the ANSWER is supported by the CONTEXT. Respond with exactly one word: YES or NO."

func (j *LLMFaithfulnessJudge) Judge(ctx context.Context, answer string, chunks []ContextChunk) (float64, error) {
	if j == nil || j.chat == nil {
		return 0, fmt.Errorf("bench: faithfulness judge is not configured")
	}
	var sb strings.Builder
	for i, c := range chunks {
		fmt.Fprintf(&sb, "[%d] (doc %s) %s\n", i+1, c.DocID, strings.TrimSpace(c.Text))
	}
	contextText := strings.TrimSpace(sb.String())
	if contextText == "" {
		return 0, fmt.Errorf("bench: faithfulness judge has no context")
	}
	resp, err := j.chat.Chat(ctx, llm.ChatRequest{
		Model: j.model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: faithfulnessSystemPrompt},
			{Role: "user", Content: fmt.Sprintf("ANSWER:\n%s\n\nCONTEXT:\n%s", answer, contextText)},
		},
	})
	if err != nil {
		return 0, err
	}
	return parseFaithfulness(resp.Content)
}

func parseFaithfulness(content string) (float64, error) {
	trimmed := strings.ToLower(strings.TrimSpace(content))
	switch {
	case trimmed == "":
		return 0, fmt.Errorf("bench: empty faithfulness verdict")
	case strings.HasPrefix(trimmed, "yes"):
		return 1, nil
	case strings.HasPrefix(trimmed, "no"):
		return 0, nil
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(content), 64); err == nil {
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		return v, nil
	}
	return 0, fmt.Errorf("bench: unparseable faithfulness verdict %q", content)
}
