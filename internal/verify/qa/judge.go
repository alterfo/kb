package qa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/alterfo/kb/internal/llm"
)

const DefaultOverlapThreshold = 0.35

type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

type Verdict struct {
	Passed bool   `json:"passed"`
	Reason string `json:"reason"`
}

type Judge interface {
	Judge(ctx context.Context, question, answer, expected string) (Verdict, error)
}

const judgeSystemPrompt = `You compare an answer produced by a knowledge base with the expected solution for a closed GitHub issue. ` +
	`The answer passes when it addresses the question and is factually consistent with the expected solution. ` +
	`Answer with JSON only: {"passed": true or false, "reason": "short explanation"}. No prose, no markdown fences.`

type LLMJudge struct {
	Chat  ChatClient
	Model string
}

func NewLLMJudge(chat ChatClient, model string) *LLMJudge {
	return &LLMJudge{Chat: chat, Model: model}
}

func (j *LLMJudge) Judge(ctx context.Context, question, answer, expected string) (Verdict, error) {
	if j == nil || j.Chat == nil {
		return Verdict{}, errors.New("qa: judge chat client is unavailable")
	}
	resp, err := j.Chat.Chat(ctx, llm.ChatRequest{
		Model: j.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: judgeSystemPrompt},
			{Role: "user", Content: judgePrompt(question, answer, expected)},
		},
	})
	if err != nil {
		return Verdict{}, err
	}
	verdict, err := parseVerdict(resp.Content)
	if err != nil {
		return Verdict{}, err
	}
	return verdict, nil
}

func judgePrompt(question, answer, expected string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Question:\n%s\n\nAnswer:\n%s\n\nExpected solution:\n%s\n", question, answer, expected)
	return b.String()
}

func parseVerdict(content string) (Verdict, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var v Verdict
	if err := json.Unmarshal([]byte(content), &v); err != nil {
		return Verdict{}, fmt.Errorf("qa: parse judge verdict: %w", err)
	}
	return v, nil
}

var tokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

func Overlap(a, b string) float64 {
	as := tokenSet(a)
	bs := tokenSet(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	union := make(map[string]struct{}, len(as)+len(bs))
	for t := range as {
		union[t] = struct{}{}
	}
	for t := range bs {
		union[t] = struct{}{}
	}
	intersection := 0
	for t := range as {
		if _, ok := bs[t]; ok {
			intersection++
		}
	}
	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

func tokenSet(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, token := range tokenRe.FindAllString(strings.ToLower(s), -1) {
		out[token] = struct{}{}
	}
	return out
}
