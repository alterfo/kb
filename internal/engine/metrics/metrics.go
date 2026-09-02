package metrics

import (
	"time"

	"github.com/alterfo/kb/internal/engine/chunk"
	"github.com/alterfo/kb/internal/store/vector"
)

// Cost is an estimated LLM token cost for one retrieval/Ask run. The
// project's OpenAI-compatible client does not expose provider usage data,
// so tokens are estimated from request and response text lengths. Cost in
// provider currency can be derived by callers from these token counts.
type Cost struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Values is the per-run observability payload attached to retrieval and Ask
// responses. RecallAtK is zero unless the caller supplied relevance labels.
type Values struct {
	RecallAtK float64 `json:"recall_at_k"`
	LatencyMS int64   `json:"latency_ms"`
	Cost      Cost    `json:"cost"`
}

// EstimateTokens estimates token count from rune count, matching the
// existing chunk token estimator.
func EstimateTokens(text string) int {
	return chunk.EstimateTokens(text)
}

// EstimateChatCost estimates the token cost of a single chat completion
// from its prompt and completion text.
func EstimateChatCost(prompt, completion string) Cost {
	promptTokens := EstimateTokens(prompt)
	completionTokens := EstimateTokens(completion)
	return Cost{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
}

// Add accumulates another cost into c.
func (c *Cost) Add(other Cost) {
	c.PromptTokens += other.PromptTokens
	c.CompletionTokens += other.CompletionTokens
	c.TotalTokens += other.TotalTokens
}

// ComputeRecallAtK returns the fraction of relevant documents present in
// the first k retrieved chunks. A nil or empty relevance set returns 1,
// matching the existing regression-gate semantics.
func ComputeRecallAtK(hits []vector.ScoredChunk, relevant map[string]struct{}, k int) float64 {
	if len(relevant) == 0 {
		return 1
	}
	if k > len(hits) {
		k = len(hits)
	}
	if k <= 0 {
		return 0
	}
	found := 0
	for i := 0; i < k; i++ {
		if _, ok := relevant[hits[i].RefDocID]; ok {
			found++
		}
	}
	return float64(found) / float64(len(relevant))
}

// RelevantSet converts relevance IDs into a lookup set.
func RelevantSet(ids []string) map[string]struct{} {
	if len(ids) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// LatencyMS returns the number of milliseconds elapsed since start.
func LatencyMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
