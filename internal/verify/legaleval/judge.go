package legaleval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alterfo/kb/internal/llm"
)

type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

const statuteRelevanceSystemPrompt = `You judge whether a legal article actually regulates the subject of a given legal question. ` +
	`Answer with JSON only: {"passed": true or false, "reason": "short explanation"}. No prose, no markdown fences.`

const claimTruthfulnessSystemPrompt = `You judge whether the claims made in an answer are supported by the provided legal evidence ` +
	`(code article texts and Plenum clarifications). Every statement of law must be traceable to the evidence. ` +
	`Answer with JSON only: {"passed": true or false, "reason": "short explanation"}. No prose, no markdown fences.`

type LLMJudge struct {
	Chat  ChatClient
	Model string
}

func (j *LLMJudge) StatuteRelevant(ctx context.Context, question string, article Article) (Verdict, error) {
	if j == nil || j.Chat == nil {
		return Verdict{}, fmt.Errorf("legaleval: judge chat unavailable")
	}
	resp, err := j.Chat.Chat(ctx, llm.ChatRequest{
		Model: j.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: statuteRelevanceSystemPrompt},
			{Role: "user", Content: statuteRelevancePrompt(question, article)},
		},
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("legaleval: statute relevance judge: %w", err)
	}
	return parseVerdict(resp.Content)
}

func (j *LLMJudge) ClaimTruthful(ctx context.Context, question, answer string, evidence []string) (Verdict, error) {
	if j == nil || j.Chat == nil {
		return Verdict{}, fmt.Errorf("legaleval: judge chat unavailable")
	}
	resp, err := j.Chat.Chat(ctx, llm.ChatRequest{
		Model: j.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: claimTruthfulnessSystemPrompt},
			{Role: "user", Content: claimTruthfulnessPrompt(question, answer, evidence)},
		},
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("legaleval: claim truthfulness judge: %w", err)
	}
	return parseVerdict(resp.Content)
}

func statuteRelevancePrompt(question string, article Article) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nArticle: %s (%s)\n", question, article.ID, article.Number)
	if article.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", article.Title)
	}
	fmt.Fprintf(&b, "Text:\n%s\n", article.Body)
	return b.String()
}

func claimTruthfulnessPrompt(question, answer string, evidence []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nAnswer:\n%s\n", question, answer)
	if len(evidence) == 0 {
		b.WriteString("\nEvidence: none\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\nEvidence:\n")
	for _, e := range evidence {
		fmt.Fprintf(&b, "- %s\n", e)
	}
	return b.String()
}

func parseVerdict(content string) (Verdict, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var v struct {
		Passed bool   `json:"passed"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content), &v); err != nil {
		return Verdict{}, fmt.Errorf("legaleval: parse judge verdict: %w", err)
	}
	return Verdict{Passed: v.Passed, Detail: v.Reason}, nil
}
