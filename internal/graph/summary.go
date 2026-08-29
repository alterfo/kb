package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
)

// Summarizer produces a titled summary for a community of entities via an
// LLM chat completion. Fail-open: on any error, empty response, or
// unparseable JSON it falls back to a deterministic title built from member
// names and an empty summary.
type Summarizer struct {
	Chat  ChatClient
	Model string
}

func NewSummarizer(chat ChatClient, model string) *Summarizer {
	return &Summarizer{Chat: chat, Model: model}
}

const summarySystemPrompt = `You summarize a cluster of related entities and their relations from a knowledge graph. ` +
	`Respond with JSON: {"title":"short title","summary":"a few sentences"}. No prose, no markdown fences.`

func (s *Summarizer) Summarize(ctx context.Context, entities []graphstore.Entity, relations []graphstore.Relation) (string, string) {
	fallbackTitle := fallbackTitle(entities)

	if s == nil || s.Chat == nil || len(entities) == 0 {
		return fallbackTitle, ""
	}

	resp, err := s.Chat.Chat(ctx, llm.ChatRequest{
		Model: s.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: summarySystemPrompt},
			{Role: "user", Content: buildSummaryPrompt(entities, relations)},
		},
	})
	if err != nil {
		return fallbackTitle, ""
	}

	title, summary, ok := parseSummary(resp.Content)
	if !ok {
		return fallbackTitle, ""
	}
	if title == "" {
		title = fallbackTitle
	}
	return title, summary
}

func fallbackTitle(entities []graphstore.Entity) string {
	names := make([]string, 0, len(entities))
	for _, e := range entities {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	if len(names) > 3 {
		names = names[:3]
	}
	return strings.Join(names, ", ")
}

func buildSummaryPrompt(entities []graphstore.Entity, relations []graphstore.Relation) string {
	var b strings.Builder
	b.WriteString("Entities:\n")
	for _, e := range entities {
		fmt.Fprintf(&b, "- %s (%s): %s\n", e.Name, e.Type, e.Description)
	}
	b.WriteString("Relations:\n")
	for _, r := range relations {
		fmt.Fprintf(&b, "- %s -[%s]-> %s: %s\n", r.Src, r.Type, r.Dst, r.Description)
	}
	return b.String()
}

type summaryJSON struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

func parseSummary(content string) (string, string, bool) {
	content = stripCodeFence(content)
	content = stripTrailingCommas(content)

	var parsed summaryJSON
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return "", "", false
	}
	return strings.TrimSpace(parsed.Title), strings.TrimSpace(parsed.Summary), true
}
