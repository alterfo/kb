package planner

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/llm"
)

type fakeChat struct {
	script  []llm.ChatResponse
	calls   int
	lastReq llm.ChatRequest
}

func (f *fakeChat) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.lastReq = req
	if f.calls >= len(f.script) {
		return llm.ChatResponse{}, fmt.Errorf("fakeChat: no scripted response for call %d", f.calls)
	}
	r := f.script[f.calls]
	f.calls++
	return r, nil
}

func toolCall(id, name, args string) llm.ToolCall {
	var tc llm.ToolCall
	tc.ID = id
	tc.Type = "function"
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

func TestAgent_ExecutesToolCallThenReturns(t *testing.T) {
	fc := &fakeChat{script: []llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{toolCall("call_1", "bash", `{"command":"printf hi"}`)}},
		{Content: "all done"},
	}}
	a := NewAgent(fc, "m", "sys", t.TempDir())
	got, err := a.Run(context.Background(), []llm.ChatMessage{{Role: "user", Content: "do it"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "all done" {
		t.Errorf("got %q", got)
	}
	if fc.calls != 2 {
		t.Errorf("expected 2 chat calls, got %d", fc.calls)
	}
	var sawToolMsg bool
	for _, m := range fc.lastReq.Messages {
		if m.Role == "tool" && m.ToolCallID == "call_1" && m.Content == "hi" {
			sawToolMsg = true
		}
	}
	if !sawToolMsg {
		t.Errorf("expected tool result message, got %+v", fc.lastReq.Messages)
	}
}

func TestAgent_NoToolCallReturnsImmediately(t *testing.T) {
	fc := &fakeChat{script: []llm.ChatResponse{{Content: "answer"}}}
	a := NewAgent(fc, "m", "sys", t.TempDir())
	got, err := a.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "answer" {
		t.Errorf("got %q", got)
	}
	if fc.calls != 1 {
		t.Errorf("expected 1 call, got %d", fc.calls)
	}
}

func TestAgent_ToolErrorIsFedBack(t *testing.T) {
	fc := &fakeChat{script: []llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{toolCall("call_1", "unknown_tool", `{}`)}},
		{Content: "recovered"},
	}}
	a := NewAgent(fc, "m", "sys", t.TempDir())
	got, err := a.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "recovered" {
		t.Errorf("got %q", got)
	}
	var sawErrMsg bool
	for _, m := range fc.lastReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "unknown tool") {
			sawErrMsg = true
		}
	}
	if !sawErrMsg {
		t.Errorf("expected tool error message, got %+v", fc.lastReq.Messages)
	}
}

func TestSignal_Detection(t *testing.T) {
	if s := findSignal("nothing here"); s != "" {
		t.Errorf("unexpected signal %q", s)
	}
	if s := findSignal("work done <<<PLANNER:ALL_TASKS_DONE>>>"); s != SignalAllTasksDone {
		t.Errorf("signal = %q", s)
	}
	if !HasSignal("<<<PLANNER:TASK_FAILED>>>", SignalTaskFailed) {
		t.Error("expected HasSignal true")
	}
}
