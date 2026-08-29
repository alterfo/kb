package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
)

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	Tools       []Tool        `json:"tools,omitempty"`
}

type ChatResponse struct {
	Content      string
	FinishReason string
	ToolCalls    []ToolCall
}

type ChatChunk struct {
	Content string
	Done    bool
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
	Tools       []Tool        `json:"tools,omitempty"`
}

type chatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

type chatStreamChunk struct {
	Choices []struct {
		Delta        chatMessage `json:"delta"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

func (c *Client) chatPayload(req ChatRequest, stream bool) chatCompletionRequest {
	return chatCompletionRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		MaxTokens:   c.maxTokens,
		Stream:      stream,
		Tools:       req.Tools,
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	payload := c.chatPayload(req, false)
	resp, err := c.postJSON(ctx, "/v1/chat/completions", payload)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("chat: unexpected status %s: %s", resp.Status, string(b))
	}

	var parsed chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ChatResponse{}, fmt.Errorf("chat: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("chat: empty choices in response")
	}
	choice := parsed.Choices[0]
	return ChatResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		ToolCalls:    choice.Message.ToolCalls,
	}, nil
}

func (c *Client) ChatStream(ctx context.Context, req ChatRequest) (iter.Seq2[ChatChunk, error], error) {
	payload := c.chatPayload(req, true)
	resp, err := c.postJSON(ctx, "/v1/chat/completions", payload)
	if err != nil {
		return nil, fmt.Errorf("chat stream request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("chat stream: unexpected status %s: %s", resp.Status, string(b))
	}

	return func(yield func(ChatChunk, error) bool) {
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				yield(ChatChunk{Done: true}, nil)
				return
			}
			var chunk chatStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				if !yield(ChatChunk{}, fmt.Errorf("chat stream: decode chunk: %w", err)) {
					return
				}
				continue
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			if chunk.Choices[0].FinishReason != "" {
				yield(ChatChunk{Done: true}, nil)
				return
			}
			if !yield(ChatChunk{Content: chunk.Choices[0].Delta.Content}, nil) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			yield(ChatChunk{}, fmt.Errorf("chat stream: read: %w", err))
		}
	}, nil
}
