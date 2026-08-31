package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChat_ParsesNonStreamResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var req chatCompletionRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			t.Error("expected Stream=false for Chat()")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"content": "hello there", "role": "assistant"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	resp, err := c.Chat(context.Background(), ChatRequest{
		Model:    "qwen3.8:latest",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hello there" {
		t.Errorf("unexpected content %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("unexpected finish reason %q", resp.FinishReason)
	}
}

func TestChat_NoThink_UsesNativeAPIChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %s, want /api/chat", r.URL.Path)
		}
		var req nativeChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Think {
			t.Error("expected think=false in native request")
		}
		if req.Stream {
			t.Error("expected stream=false in native request")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"message":     map[string]string{"content": "4", "role": "assistant"},
			"done_reason": "stop",
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, NoThink: true})
	resp, err := c.Chat(context.Background(), ChatRequest{
		Model:    "qwen3.8:latest",
		Messages: []ChatMessage{{Role: "user", Content: "2+2="}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "4" {
		t.Errorf("unexpected content %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("unexpected finish reason %q", resp.FinishReason)
	}
}

func TestChat_NoThink_SendsMaxTokensAsNumPredict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nativeChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Options == nil || req.Options.NumPredict != 256 {
			t.Errorf("expected options.num_predict=256, got %+v", req.Options)
		}
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": "ok"}})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, NoThink: true, MaxTokens: 256})
	if _, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_NoThink_OmitsOptionsWhenMaxTokensUnset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nativeChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Options != nil {
			t.Errorf("expected no options when MaxTokens is unset, got %+v", req.Options)
		}
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": "ok"}})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, NoThink: true})
	if _, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_NoThink_FallsBackToOpenAICompatWhenToolsPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s, want /v1/chat/completions (tool calls must not use native endpoint)", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"content": "ok", "role": "assistant"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, NoThink: true})
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "qwen3.8:latest",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:    []Tool{{Type: "function"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_EmptyContent_ReturnsEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": ""}}},
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	resp, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("expected empty content, got %q", resp.Content)
	}
}

func TestChat_Non200Status_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	_, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestChat_RetriesOn5xxThenSucceeds(t *testing.T) {
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := NewClient(Config{
		BaseURL:    srv.URL,
		MaxRetries: 2,
		BaseDelay:  time.Millisecond,
		MaxDelay:   2 * time.Millisecond,
	})
	resp, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("unexpected content %q", resp.Content)
	}
	if attempt != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempt)
	}
}

func TestChat_TimesOutOnSlowServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "too late"}}},
		})
	}))
	defer srv.Close()

	c := NewClient(Config{
		BaseURL:        srv.URL,
		MaxRetries:     0,
		RequestTimeout: 20 * time.Millisecond,
	})
	start := time.Now()
	_, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("expected fast timeout failure, took %v", elapsed)
	}
}

func TestChat_CancelledContext_ReturnsImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "x"}}},
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Chat(ctx, ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestChatStream_ParsesSSEChunksAndDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			t.Error("expected Stream=true for ChatStream()")
		}
		if len(req.Tools) != 1 || req.Tools[0].Function.Name != "lookup" {
			t.Errorf("expected Tools to be forwarded to the streaming request, got %+v", req.Tools)
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	tool := Tool{Function: ToolFunction{Name: "lookup", Description: "look things up", Parameters: map[string]any{"type": "object"}}}
	seq, err := c.ChatStream(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}, Tools: []Tool{tool}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var gotContent string
	var chunkCount int
	var sawDone bool
	for chunk, err := range seq {
		if err != nil {
			t.Fatalf("unexpected chunk error: %v", err)
		}
		chunkCount++
		gotContent += chunk.Content
		if chunk.Done && chunk.Content == "" {
			sawDone = true
		}
	}
	if gotContent != "Hello" {
		t.Errorf("expected concatenated content %q, got %q", "Hello", gotContent)
	}
	if !sawDone {
		t.Error("expected a terminal Done chunk")
	}
	if chunkCount != 3 {
		t.Errorf("expected 3 chunks (2 content + done), got %d", chunkCount)
	}
}

func TestChatStream_MalformedChunk_YieldsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {not-json}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	seq, err := c.ChatStream(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var sawErr bool
	for _, err := range seq {
		if err != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("expected an error to be yielded for malformed JSON chunk")
	}
}

func TestChatStream_Non200Status_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, MaxRetries: 0, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond})
	_, err := c.ChatStream(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestChatStream_EarlyBreak_StopsIteration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	seq, err := c.ChatStream(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var count int
	for range seq {
		count++
		break
	}
	if count != 1 {
		t.Fatalf("expected iteration to stop after first chunk, got %d", count)
	}
}

func TestChat_SendsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "sk-test-key"})
	if _, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotAuth != "Bearer sk-test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer sk-test-key")
	}
}

func TestChat_ParsesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) != 1 {
			t.Errorf("expected 1 tool in request, got %d", len(req.Tools))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": "",
					"tool_calls": []map[string]any{{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      "read_file",
							"arguments": map[string]string{"path": "a.go"},
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	resp, err := c.Chat(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:    []Tool{NewTool("read_file", "read a file", map[string]any{})},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "read_file" || tc.Function.Arguments != `{"path":"a.go"}` {
		t.Errorf("unexpected tool call: %+v", tc)
	}
}

func TestToolCall_ArgumentsAsJSONString(t *testing.T) {
	var tc ToolCall
	if err := json.Unmarshal([]byte(`{"id":"c1","function":{"name":"f","arguments":"{\"a\":1}"}}`), &tc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tc.Function.Arguments != `{"a":1}` {
		t.Errorf("unexpected arguments %q", tc.Function.Arguments)
	}
}
