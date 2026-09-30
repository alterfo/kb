package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

const naiveSystemPrompt = "Answer the question using only the provided source excerpts. Cite source document IDs where relevant."

type Retriever interface {
	Retrieve(ctx context.Context, query string, opt retriever.Options) ([]vector.ScoredChunk, error)
}

type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

func NaiveAnswer(ctx context.Context, r Retriever, chat ChatClient, model string, topK int, text string) (string, []string, error) {
	answer, docIDs, _, err := NaiveAnswerWithContext(ctx, r, chat, model, topK, text)
	return answer, docIDs, err
}

func NaiveAnswerWithContext(ctx context.Context, r Retriever, chat ChatClient, model string, topK int, text string) (string, []string, []ContextChunk, error) {
	chunks, err := r.Retrieve(ctx, text, retriever.Options{K: topK})
	if err != nil {
		return "", nil, nil, err
	}

	var sources strings.Builder
	docIDs := make([]string, 0, len(chunks))
	contextChunks := make([]ContextChunk, 0, len(chunks))
	seen := make(map[string]struct{}, len(chunks))
	for i, sc := range chunks {
		docID := sc.RefDocID
		if id := sc.Metadata["id"]; id != "" {
			docID = id
		}
		if docID != "" {
			if _, ok := seen[docID]; !ok {
				seen[docID] = struct{}{}
				docIDs = append(docIDs, docID)
			}
		}
		contextChunks = append(contextChunks, ContextChunk{DocID: docID, Text: sc.Text})
		fmt.Fprintf(&sources, "\n[%d] (doc %s) %s", i+1, docID, strings.TrimSpace(sc.Text))
	}

	sourceText := strings.TrimSpace(sources.String())
	if sourceText == "" {
		sourceText = "(no source excerpts retrieved)"
	}

	resp, err := chat.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: naiveSystemPrompt},
			{Role: "user", Content: fmt.Sprintf("Question: %s\n\nSources:\n%s", text, sourceText)},
		},
	})
	if err != nil {
		return "", nil, nil, err
	}
	return strings.TrimSpace(resp.Content), docIDs, contextChunks, nil
}
