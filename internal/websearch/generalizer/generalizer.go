package generalizer

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/websearch/guard"
)

type Chat interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

type Guard interface {
	Check(query string) (guard.Verdict, error)
}

type BlockedError struct {
	Reasons []guard.Reason
}

func (e *BlockedError) Error() string {
	return "generalizer: query still blocked: " + joinReasons(e.Reasons)
}

var ErrEmpty = errors.New("generalizer: model returned empty query")

type Generalizer struct {
	Guard Guard
	Model string
}

func New(g Guard, model string) *Generalizer {
	return &Generalizer{Guard: g, Model: model}
}

const systemPrompt = `You rewrite a user's follow-up question into a single, safe web search query.
Return ONLY the search query, no explanation, no quotes, no markdown fences.
Generalize the question while preserving its meaning.
Remove every name, email, phone number, identifier, secret, internal project name, filesystem path, and private host.`

func (g *Generalizer) Propose(ctx context.Context, chat Chat, question, answerContext string) (string, error) {
	if g == nil {
		return "", errors.New("generalizer: nil receiver")
	}
	if chat == nil {
		return "", errors.New("generalizer: nil chat client")
	}
	if g.Guard == nil {
		return "", errors.New("generalizer: nil guard")
	}

	messages := buildMessages(question, answerContext)
	query, verdict, err := attempt(ctx, chat, g.Model, g.Guard, messages)
	if err != nil {
		return "", err
	}
	if verdict.Allowed {
		return query, nil
	}

	messages = append(messages,
		llm.ChatMessage{Role: "assistant", Content: query},
		llm.ChatMessage{Role: "user", Content: retryInstruction(verdict.Reasons)},
	)
	query, verdict, err = attempt(ctx, chat, g.Model, g.Guard, messages)
	if err != nil {
		return "", err
	}
	if !verdict.Allowed {
		return "", &BlockedError{Reasons: verdict.Reasons}
	}
	return query, nil
}

func buildMessages(question, answerContext string) []llm.ChatMessage {
	user := question
	if strings.TrimSpace(answerContext) != "" {
		user = "Question:\n" + question + "\n\nRelevant context, for understanding only; never leak its internal details:\n" + answerContext
	}
	return []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: user, Untrusted: true},
	}
}

func attempt(ctx context.Context, chat Chat, model string, g Guard, messages []llm.ChatMessage) (string, guard.Verdict, error) {
	resp, err := chat.Chat(ctx, llm.ChatRequest{Model: model, Messages: messages})
	if err != nil {
		return "", guard.Verdict{}, fmt.Errorf("generalize: %w", err)
	}
	query := cleanQuery(resp.Content)
	if strings.TrimSpace(query) == "" {
		return "", guard.Verdict{}, ErrEmpty
	}
	verdict, err := g.Check(query)
	if err != nil {
		return "", guard.Verdict{}, fmt.Errorf("generalize guard: %w", err)
	}
	return query, verdict, nil
}

var thinkRE = regexp.MustCompile(`(?s)<think>.*?(?:</think>|$)`)

func cleanQuery(s string) string {
	s = thinkRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	for {
		switch {
		case strings.HasPrefix(s, "```"):
			s = strings.TrimSpace(strings.TrimPrefix(s, "```"))
			if idx := strings.LastIndex(s, "```"); idx >= 0 {
				s = strings.TrimSpace(s[:idx])
			}
		case len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '`' && s[len(s)-1] == '`')):
			s = strings.TrimSpace(s[1 : len(s)-1])
		default:
			return s
		}
	}
}

func retryInstruction(reasons []guard.Reason) string {
	return "That query was blocked by the safety filter for: " + joinReasons(reasons) + ". " +
		"Rewrite it once more as a single safe web search query without any of those elements."
}

func joinReasons(reasons []guard.Reason) string {
	parts := make([]string, len(reasons))
	for i, r := range reasons {
		parts[i] = string(r)
	}
	return strings.Join(parts, ", ")
}
