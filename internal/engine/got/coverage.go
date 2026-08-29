package got

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

const coverageJudgeSystemPrompt = `You judge whether the given excerpts sufficiently answer a sub-question. ` +
	`Respond with JSON {"covered":true|false,"score":0.0-1.0}. No prose, no markdown fences.`

type coverageResult struct {
	Score   float64
	Covered bool
}

// scoreCoverage decides whether the retrieved chunks cover query. It first
// computes a deterministic score from chunk count and relevance; only when
// that score falls in the uncertain middle band does it spend an LLM call
// to judge coverage directly. Fail-open on any LLM error: the deterministic
// score decides.
func (o *Orchestrator) scoreCoverage(ctx context.Context, query string, chunks []vector.ScoredChunk) coverageResult {
	det := deterministicCoverage(chunks, o.cfg.K)
	if det >= o.cfg.CoverageHigh {
		return coverageResult{Score: det, Covered: true}
	}
	if det <= o.cfg.CoverageLow || o.cfg.Chat == nil {
		return coverageResult{Score: det, Covered: false}
	}

	resp, ok := o.chat(ctx, llm.ChatRequest{
		Model: o.cfg.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: coverageJudgeSystemPrompt},
			{Role: "user", Content: buildCoveragePrompt(query, chunks)},
		},
	})
	if !ok {
		return coverageResult{Score: det, Covered: false}
	}
	judged, ok := parseCoverageJudgment(resp.Content)
	if !ok {
		return coverageResult{Score: det, Covered: false}
	}
	return judged
}

func deterministicCoverage(chunks []vector.ScoredChunk, k int) float64 {
	if len(chunks) == 0 {
		return 0
	}
	if k <= 0 {
		k = 1
	}
	countRatio := float64(len(chunks)) / float64(k)
	if countRatio > 1 {
		countRatio = 1
	}
	var sum float64
	for _, c := range chunks {
		sum += c.Score
	}
	avgScore := sum / float64(len(chunks))
	if avgScore < 0 {
		avgScore = 0
	}
	if avgScore > 1 {
		avgScore = 1
	}
	return (countRatio + avgScore) / 2
}

func buildCoveragePrompt(query string, chunks []vector.ScoredChunk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Sub-question: %s\n\nExcerpts:\n", query)
	for _, c := range chunks {
		fmt.Fprintf(&b, "- (%s) %s\n", c.FileName, c.Text)
	}
	return b.String()
}

func parseCoverageJudgment(content string) (coverageResult, bool) {
	content = stripCodeFence(content)

	var parsed struct {
		Covered bool    `json:"covered"`
		Score   float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return coverageResult{}, false
	}
	if parsed.Score < 0 {
		parsed.Score = 0
	}
	if parsed.Score > 1 {
		parsed.Score = 1
	}
	return coverageResult{Score: parsed.Score, Covered: parsed.Covered}, true
}
