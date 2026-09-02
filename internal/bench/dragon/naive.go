package dragon

import (
	"context"
	"fmt"
	"strings"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

const naiveSystemPrompt = "Answer the question using only the provided source excerpts. Cite source document IDs where relevant."

// Retriever is the single-shot retrieval seam used by the naive answer
// path. Satisfied by *retriever.Retriever.
type Retriever interface {
	Retrieve(ctx context.Context, query string, opt retriever.Options) ([]vector.ScoredChunk, error)
}

// ChatClient runs a single non-streaming chat completion. Satisfied by
// *llm.Client.
type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

// NaiveAnswer is the plain single-shot RAG answering path: one retrieval,
// one chat call, no Graph-of-Thoughts decomposition or refinement. It
// returns the answer text plus the unique source document IDs (first-seen
// order) backing the retrieved chunks.
func NaiveAnswer(ctx context.Context, r Retriever, chat ChatClient, model string, k int, query string) (string, []string, error) {
	chunks, err := r.Retrieve(ctx, query, retriever.Options{K: k})
	if err != nil {
		return "", nil, err
	}

	var sources strings.Builder
	docIDs := make([]string, 0, len(chunks))
	seen := make(map[string]struct{}, len(chunks))
	for i, sc := range chunks {
		if sc.RefDocID != "" {
			if _, ok := seen[sc.RefDocID]; !ok {
				seen[sc.RefDocID] = struct{}{}
				docIDs = append(docIDs, sc.RefDocID)
			}
		}
		fmt.Fprintf(&sources, "\n[%d] (doc %s) %s", i+1, sc.RefDocID, strings.TrimSpace(sc.Text))
	}

	sourceText := strings.TrimSpace(sources.String())
	if sourceText == "" {
		sourceText = "(no source excerpts retrieved)"
	}

	resp, err := chat.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: naiveSystemPrompt},
			{Role: "user", Content: fmt.Sprintf("Question: %s\n\nSources:\n%s", query, sourceText)},
		},
	})
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(resp.Content), docIDs, nil
}
