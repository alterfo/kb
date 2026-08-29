package report

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
)

const globalReportSystemPrompt = `You write a global knowledge-base report answering the user's question by ` +
	`synthesizing community summaries from a knowledge graph. Reference community titles for grounding. If the ` +
	`summaries don't contain the answer, say so plainly. No markdown fences.`

// GlobalReport answers query GraphRAG-style: it treats every community
// summary as a partial answer and asks the LLM to synthesize them into one
// report. Fails open to a plain listing of community titles when chat is
// nil, there are no (summarized) communities, the chat call errors, or the
// reply is empty.
func GlobalReport(ctx context.Context, chat ChatClient, model, query string, communities []graphstore.Community) string {
	communities = withSummaries(communities)
	if chat == nil || len(communities) == 0 {
		return fallbackGlobalReport(communities)
	}

	resp, err := chat.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: globalReportSystemPrompt},
			{Role: "user", Content: buildGlobalReportPrompt(query, communities)},
		},
	})
	if err != nil || strings.TrimSpace(resp.Content) == "" {
		return fallbackGlobalReport(communities)
	}
	return resp.Content
}

func withSummaries(communities []graphstore.Community) []graphstore.Community {
	out := make([]graphstore.Community, 0, len(communities))
	for _, c := range communities {
		if strings.TrimSpace(c.Summary) != "" {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func buildGlobalReportPrompt(query string, communities []graphstore.Community) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nCommunity summaries:\n", query)
	for _, c := range communities {
		fmt.Fprintf(&b, "- %s: %s\n", c.Title, c.Summary)
	}
	return b.String()
}

func fallbackGlobalReport(communities []graphstore.Community) string {
	if len(communities) == 0 {
		return "no community summaries found"
	}
	titles := make([]string, 0, len(communities))
	for _, c := range communities {
		titles = append(titles, c.Title)
	}
	return "relevant communities found but report synthesis unavailable: " + strings.Join(titles, ", ")
}
