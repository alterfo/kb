package report

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

// ChatClient runs a single chat completion. Satisfied by *llm.Client.
type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

// NotFoundSentinel is the canonical deterministic "not in the knowledge
// base" answer shared by the search and ask paths.
const NotFoundSentinel = "no information found"

const searchSynthesisSystemPrompt = `You answer a user's search query using ONLY the provided excerpts from a ` +
	`knowledge base. Lead with the direct answer including exact numbers, dates and names as they appear in the excerpts. ` +
	`Then list every supporting fact as a separate bullet. Cite sources by file name in parentheses after each claim. ` +
	`If excerpts conflict, state both versions explicitly with their file names instead of picking silently. ` +
	`Never invent numbers or facts. If the excerpts don't contain the answer, say so plainly. No markdown fences.`

// maxSynthesisAttempts bounds the initial call plus one retry with a
// trimmed context.
const maxSynthesisAttempts = 2

// Synthesize produces a grounded answer to query from chunks, citing
// sources by file name. Fails open to a plain listing of the source file
// names when chat is nil, chunks is empty, the chat call errors, or the
// reply is empty.
func Synthesize(ctx context.Context, chat ChatClient, model, query string, chunks []vector.ScoredChunk) string {
	text, _, _ := SynthesizeResult(ctx, chat, model, query, chunks)
	return text
}

// SynthesizeResult is Synthesize with explicit fallback reporting. On an
// LLM error or an empty reply it retries once with a halved chunk set and
// returns a human-readable FallbackReason instead of just a bool.
func SynthesizeResult(ctx context.Context, chat ChatClient, model, query string, chunks []vector.ScoredChunk) (text string, fallback bool, fallbackReason string) {
	if chat == nil {
		return fallbackNote(chunks), true, "synthesis unavailable: no chat client configured"
	}
	if len(chunks) == 0 {
		return fallbackNote(chunks), true, "synthesis unavailable: no chunks retrieved"
	}

	attemptChunks := chunks
	for attempt := 0; attempt < maxSynthesisAttempts; attempt++ {
		resp, err := chat.Chat(ctx, llm.ChatRequest{
			Model: model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: searchSynthesisSystemPrompt},
				{Role: "user", Content: buildSynthesisPrompt(query, attemptChunks)},
			},
		})
		if err == nil && strings.TrimSpace(resp.Content) != "" {
			return resp.Content, false, ""
		}

		if err != nil {
			slog.Error("synthesis attempt failed", "attempt", attempt+1, "chunks", len(attemptChunks), "error", err)
		} else {
			slog.Error("synthesis attempt returned empty response", "attempt", attempt+1, "chunks", len(attemptChunks))
		}

		attemptChunks = trimSynthesisChunks(attemptChunks)
		if len(attemptChunks) == 0 {
			break
		}
	}

	reason := fallbackReasonFor(chunks)
	slog.Error("synthesis failed; falling back to source list", "reason", reason)
	return fallbackNote(chunks), true, reason
}

func trimSynthesisChunks(chunks []vector.ScoredChunk) []vector.ScoredChunk {
	if len(chunks) < 2 {
		return nil
	}
	return chunks[:len(chunks)/2]
}

func fallbackReasonFor(chunks []vector.ScoredChunk) string {
	return fmt.Sprintf("synthesis failed after %d attempt(s) over %d chunk(s)", maxSynthesisAttempts, len(chunks))
}

func buildSynthesisPrompt(query string, chunks []vector.ScoredChunk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Query: %s\n\nExcerpts:\n", query)
	for _, c := range chunks {
		note := ""
		if c.SupersededBy != "" {
			note = " [superseded]"
		}
		fmt.Fprintf(&b, "- (%s)%s %s\n", c.FileName, note, c.Text)
	}
	if block := SupersessionBlock(chunks); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
	}
	return b.String()
}

func fallbackNote(chunks []vector.ScoredChunk) string {
	if len(chunks) == 0 {
		return NotFoundSentinel
	}
	seen := make(map[string]bool)
	var names []string
	for _, c := range chunks {
		if !seen[c.FileName] {
			seen[c.FileName] = true
			names = append(names, c.FileName)
		}
	}
	return "relevant sources found but synthesis unavailable: " + strings.Join(names, ", ")
}
