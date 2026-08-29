package planner

import (
	"context"
	"fmt"

	"github.com/alterfo/kb/internal/llm"
)

// ChatClient runs a single chat completion with optional tools. Satisfied by
// *llm.Client.
type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

// Agent runs an autonomous tool-using loop over a chat backend. It sends the
// system prompt plus the caller's messages, executes any tool calls the model
// requests, feeds the results back, and returns the model's final text answer
// (which the orchestrator scans for signal markers).
type Agent struct {
	chat     ChatClient
	model    string
	system   string
	tools    *tools
	maxTurns int
}

func NewAgent(chat ChatClient, model, system string, workDir string) *Agent {
	return &Agent{
		chat:     chat,
		model:    model,
		system:   system,
		tools:    newTools(workDir),
		maxTurns: 50,
	}
}

// Run executes the tool-using loop and returns the model's final message.
func (a *Agent) Run(ctx context.Context, msgs []llm.ChatMessage) (string, error) {
	messages := make([]llm.ChatMessage, 0, len(msgs)+1)
	messages = append(messages, llm.ChatMessage{Role: "system", Content: a.system})
	messages = append(messages, msgs...)

	for turn := 0; turn < a.maxTurns; turn++ {
		resp, err := a.chat.Chat(ctx, llm.ChatRequest{
			Model:    a.model,
			Messages: messages,
			Tools:    a.tools.specs(),
		})
		if err != nil {
			return "", fmt.Errorf("agent turn %d: %w", turn, err)
		}
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}
		messages = append(messages, llm.ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			result, toolErr := a.tools.exec(ctx, tc.Function.Name, tc.Function.Arguments)
			content := result
			if toolErr != nil {
				content = "error: " + toolErr.Error()
			}
			messages = append(messages, llm.ChatMessage{Role: "tool", Content: content, ToolCallID: tc.ID})
		}
	}
	return "", fmt.Errorf("agent exceeded %d tool turns", a.maxTurns)
}
