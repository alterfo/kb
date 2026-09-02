package got

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/engine/report"
	"github.com/alterfo/kb/internal/guardrails"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

const synthesizeSystemPrompt = `You answer a focused sub-question using ONLY the provided excerpts. ` +
	`Lead with the direct answer including exact numbers, dates and names as they appear in the excerpts. ` +
	`Then list every supporting fact as a separate bullet. Cite sources by file name in parentheses after each claim. ` +
	`If excerpts conflict, state both versions explicitly with their file names instead of picking silently. ` +
	`Never invent numbers or facts. If the excerpts don't contain the answer, say so plainly. ` +
	`No markdown fences. Output the answer text only: no greetings, no offers of further ` +
	`help, no tool calls or function-call syntax.`

const aggregateSystemPrompt = `You combine sub-answers into one coherent, grounded answer to the original ` +
	`question. Lead with the direct answer including exact numbers, dates and names as they appear in the ` +
	`sub-answers, then list every supporting fact as a separate bullet. Where sub-answers conflict, state both ` +
	`versions explicitly with their file names instead of picking silently. Never invent numbers or facts. ` +
	`Preserve source citations. No markdown fences. Output the answer text only: no greetings, ` +
	`no offers of further help, no tool calls or function-call syntax.`

// maxSynthesisAttempts bounds the initial call plus one retry with a
// trimmed context.
const maxSynthesisAttempts = 2

// degenerateReplyMarkers catch two model failure modes seen in production:
// leaked tool-call syntax (the model imitates an agentic transcript instead
// of answering) and generic chatbot filler (the model responds as if making
// small talk rather than following synthesizeSystemPrompt/aggregateSystemPrompt).
// Either is worse than the deterministic fallback, so it is treated as a
// failed attempt rather than shown to the user.
var degenerateReplyMarkers = []string{
	"<function_calls",
	"<invoke ",
	"</invoke>",
	"how can i help",
	"let me know if",
	"feel free to ask",
	"just let me know",
	"i don't see a specific question",
}

// isDegenerateReply reports whether content looks like a synthesis/aggregate
// failure mode rather than a grounded answer: leaked tool-call markup or
// generic assistant chit-chat instead of a sourced answer.
func isDegenerateReply(content string) bool {
	lower := strings.ToLower(content)
	for _, marker := range degenerateReplyMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// synthesize answers query from chunks, optionally prefixed with the
// resolved answers of the subgoal's dependencies. Fails open to a plain
// listing of the source file names when chat is unavailable, errors, or
// returns nothing useful.
func (o *Orchestrator) synthesize(ctx context.Context, query string, chunks []vector.ScoredChunk, deps []subgoalResult, memory []subgoalResult) string {
	text, _, _ := o.synthesizeResult(ctx, query, chunks, deps, memory)
	return text
}

// synthesizeResult is synthesize with explicit fallback reporting. On a
// chat error or an empty reply it retries once with a halved chunk set and
// returns a human-readable fallback reason instead of just a bool.
func (o *Orchestrator) synthesizeResult(ctx context.Context, query string, chunks []vector.ScoredChunk, deps []subgoalResult, memory []subgoalResult) (text string, fallback bool, fallbackReason string) {
	if o.cfg.Chat == nil || len(chunks) == 0 {
		if o.cfg.Chat == nil {
			return fallbackAnswer(chunks), true, "synthesis unavailable: no chat client configured"
		}
		return fallbackAnswer(chunks), true, "synthesis unavailable: no chunks retrieved"
	}

	attemptChunks := chunks
	for attempt := 0; attempt < maxSynthesisAttempts; attempt++ {
		resp, ok := o.chat(ctx, llm.ChatRequest{
			Model: o.cfg.Model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: synthesizeSystemPrompt},
				{Role: "user", Content: buildSynthesizePrompt(query, attemptChunks, deps, memory)},
			},
		})
		if ok && strings.TrimSpace(resp.Content) != "" && !isDegenerateReply(resp.Content) {
			return resp.Content, false, ""
		}

		switch {
		case !ok:
			slog.Error("synthesis attempt failed", "attempt", attempt+1, "chunks", len(attemptChunks))
		case strings.TrimSpace(resp.Content) == "":
			slog.Error("synthesis attempt returned empty response", "attempt", attempt+1, "chunks", len(attemptChunks))
		default:
			slog.Error("synthesis attempt returned degenerate response", "attempt", attempt+1, "chunks", len(attemptChunks))
		}

		attemptChunks = trimSynthesisChunks(attemptChunks)
		if len(attemptChunks) == 0 {
			break
		}
	}

	reason := fmt.Sprintf("synthesis failed after %d attempt(s) over %d chunk(s)", maxSynthesisAttempts, len(chunks))
	slog.Error("synthesis failed; falling back to source list", "reason", reason)
	return fallbackAnswer(chunks), true, reason
}

func trimSynthesisChunks(chunks []vector.ScoredChunk) []vector.ScoredChunk {
	if len(chunks) < 2 {
		return nil
	}
	return chunks[:len(chunks)/2]
}

func buildSynthesizePrompt(query string, chunks []vector.ScoredChunk, deps []subgoalResult, memory []subgoalResult) string {
	var b strings.Builder
	if ctx := formatRollingMemoryContext(deps, memory); ctx != "" {
		b.WriteString(ctx)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Sub-question: %s\n\nExcerpts:\n", query)
	for _, c := range chunks {
		note := ""
		if c.SupersededBy != "" {
			note = " [superseded]"
		}
		fmt.Fprintf(&b, "- (%s)%s %s\n", c.FileName, note, guardrails.DataBlock(c.Text))
	}
	if block := report.SupersessionBlock(chunks); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
	}
	return b.String()
}

const noInformationFoundAnswer = report.NotFoundSentinel

const abstainFinalAnswer = "The knowledge base does not contain the information needed to answer this question."

func allUncovered(results []subgoalResult) bool {
	for _, r := range results {
		if r.Covered {
			return false
		}
	}
	return len(results) > 0
}

const unavailableAnswerPrefix = "relevant sources found but synthesis unavailable: "

func fallbackAnswer(chunks []vector.ScoredChunk) string {
	if len(chunks) == 0 {
		return noInformationFoundAnswer
	}
	var names []string
	seen := make(map[string]bool)
	for _, c := range chunks {
		if !seen[c.FileName] {
			seen[c.FileName] = true
			names = append(names, c.FileName)
		}
	}
	return unavailableAnswerPrefix + strings.Join(names, ", ")
}

// isFallbackAnswer reports whether answer is one of fallbackAnswer's own
// placeholder strings rather than real content, so aggregation can exclude
// it instead of quoting it as if it were a grounded sub-answer.
func isFallbackAnswer(answer string) bool {
	return answer == noInformationFoundAnswer || strings.HasPrefix(answer, unavailableAnswerPrefix)
}

// aggregate combines sub-answers into one draft. Fails open to a plain
// concatenation of the sub-answers when chat is unavailable, errors, or
// returns a degenerate reply (see isDegenerateReply).
func (o *Orchestrator) aggregate(ctx context.Context, query string, results []subgoalResult) string {
	if o.cfg.Chat != nil {
		resp, ok := o.chat(ctx, llm.ChatRequest{
			Model: o.cfg.Model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: aggregateSystemPrompt},
				{Role: "user", Content: buildAggregatePrompt(query, results)},
			},
		})
		if ok && strings.TrimSpace(resp.Content) != "" && !isDegenerateReply(resp.Content) {
			return resp.Content
		}
	}
	return fallbackAggregate(results)
}

func buildAggregatePrompt(query string, results []subgoalResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n\nSub-answers:\n", query)
	for _, r := range results {
		fmt.Fprintf(&b, "- %s: %s\n", r.Query, r.Answer)
	}
	var conflicts []string
	for _, r := range results {
		conflicts = append(conflicts, r.Contradictions...)
	}
	if len(conflicts) > 0 {
		b.WriteString("\nKnown contradictions detected between sources; state them explicitly in the answer:\n")
		for _, c := range conflicts {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	return b.String()
}

// fallbackAggregate concatenates real sub-answers, dropping placeholder
// ones (see isFallbackAnswer) so they don't dilute the draft with noise like
// "relevant sources found but synthesis unavailable: ...". If every result
// is empty or a placeholder, it falls back to the first placeholder found so
// the orchestrator's fail-open guarantee (FinalAnswer is never empty when
// any subgoal produced text) still holds.
func fallbackAggregate(results []subgoalResult) string {
	var b strings.Builder
	for _, r := range results {
		if strings.TrimSpace(r.Answer) == "" || isFallbackAnswer(r.Answer) {
			continue
		}
		fmt.Fprintf(&b, "%s\n%s\n\n", r.Query, r.Answer)
	}
	if joined := strings.TrimSpace(b.String()); joined != "" {
		return joined
	}
	for _, r := range results {
		if strings.TrimSpace(r.Answer) != "" {
			return r.Answer
		}
	}
	return ""
}

func sourcesFromChunks(chunks []vector.ScoredChunk) []Source {
	out := make([]Source, 0, len(chunks))
	for _, c := range chunks {
		docID := c.RefDocID
		if id := c.Metadata["id"]; id != "" {
			docID = id
		}
		out = append(out, Source{FileName: c.FileName, FilePath: c.FilePath, ChunkID: c.ID, DocID: docID, SupersededBy: c.SupersededBy})
	}
	return out
}

// dedupSources merges sources by file path (falling back to file name),
// keeping first-seen order stabilized by a final sort for deterministic
// output.
func dedupSources(all []Source) []Source {
	seen := make(map[string]bool)
	out := make([]Source, 0, len(all))
	for _, s := range all {
		key := s.FilePath
		if key == "" {
			key = s.FileName
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FilePath < out[j].FilePath })
	return out
}
