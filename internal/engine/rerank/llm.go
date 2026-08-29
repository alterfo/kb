package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

// ChatClient runs a single chat completion. Satisfied by *llm.Client.
type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

// maxListwise caps how many candidates go into a single listwise ranking
// prompt; anything beyond stays in its original relative order, appended
// after the reranked head.
const maxListwise = 40

const listwiseSystemPrompt = `You rank candidate passages by relevance to a search query. ` +
	`Respond with a JSON array of the candidate indices (0-based), most relevant first. ` +
	`Every index must appear exactly once. No prose, no markdown fences.`

// LLM reranks candidates via a single listwise chat completion that asks
// the model to return a relevance-ordered permutation of candidate indices.
type LLM struct {
	Chat  ChatClient
	Model string
}

func NewLLM(chat ChatClient, model string) *LLM {
	return &LLM{Chat: chat, Model: model}
}

func (l *LLM) Rerank(ctx context.Context, query string, cands []vector.ScoredChunk) ([]vector.ScoredChunk, error) {
	if l == nil || l.Chat == nil || len(cands) == 0 {
		return cands, nil
	}

	head := cands
	var tail []vector.ScoredChunk
	if len(head) > maxListwise {
		tail = head[maxListwise:]
		head = head[:maxListwise]
	}

	resp, err := l.Chat.Chat(ctx, llm.ChatRequest{
		Model: l.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: listwiseSystemPrompt},
			{Role: "user", Content: buildListwisePrompt(query, head)},
		},
	})
	if err != nil {
		return cands, nil
	}

	order := parsePermutation(resp.Content, len(head))
	if order == nil {
		return cands, nil
	}

	reordered := make([]vector.ScoredChunk, 0, len(cands))
	for _, idx := range order {
		reordered = append(reordered, head[idx])
	}
	return append(reordered, tail...), nil
}

func buildListwisePrompt(query string, cands []vector.ScoredChunk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Query: %s\n\nCandidates:\n", query)
	for i, c := range cands {
		fmt.Fprintf(&b, "[%d] %s\n", i, c.Text)
	}
	return b.String()
}

// parsePermutation parses content as a JSON array of ints and validates it
// is a permutation of 0..n-1. Returns nil (fail open) on any mismatch.
func parsePermutation(content string, n int) []int {
	content = stripCodeFence(content)

	var order []int
	if err := json.Unmarshal([]byte(content), &order); err != nil {
		return nil
	}
	if len(order) != n {
		return nil
	}
	seen := make([]bool, n)
	for _, idx := range order {
		if idx < 0 || idx >= n || seen[idx] {
			return nil
		}
		seen[idx] = true
	}
	return order
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
