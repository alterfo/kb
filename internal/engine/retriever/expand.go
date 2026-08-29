package retriever

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/alterfo/kb/internal/llm"
)

// ChatClient runs a single chat completion. Satisfied by *llm.Client.
type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

const expandSystemPrompt = `You rewrite a search query into 3-5 diverse, self-contained ` +
	`sub-queries that together cover its likely intent. Respond with a JSON array of ` +
	`strings only, no prose, no markdown fences.`

const maxSubqueries = 5

// expandQuery asks the LLM for 3-5 sub-queries covering the intent of query.
// It fails open to []string{query} on any error, empty response, or nil chat.
func expandQuery(ctx context.Context, chat ChatClient, model, query string) []string {
	if chat == nil || strings.TrimSpace(query) == "" {
		return []string{query}
	}

	resp, err := chat.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: expandSystemPrompt},
			{Role: "user", Content: query},
		},
	})
	if err != nil {
		return []string{query}
	}

	subqueries := parseSubqueries(resp.Content)
	if len(subqueries) == 0 {
		return []string{query}
	}
	if len(subqueries) > maxSubqueries {
		subqueries = subqueries[:maxSubqueries]
	}
	return subqueries
}

func parseSubqueries(content string) []string {
	content = stripCodeFence(content)

	var list []string
	if err := json.Unmarshal([]byte(content), &list); err == nil {
		return cleanList(list)
	}

	var wrapped struct {
		Subqueries []string `json:"subqueries"`
	}
	if err := json.Unmarshal([]byte(content), &wrapped); err == nil {
		return cleanList(wrapped.Subqueries)
	}

	return nil
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
