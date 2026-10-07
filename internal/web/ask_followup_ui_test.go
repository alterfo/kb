package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/store/history"
)

func seedFollowupThread(t *testing.T, te *testEnv, runID string) {
	t.Helper()
	ctx := context.Background()
	if err := te.history.AppendAskMessage(ctx, history.AskMessage{RunID: runID, Role: history.AskRoleUser, Content: "What about costs?"}); err != nil {
		t.Fatalf("AppendAskMessage user: %v", err)
	}
	if err := te.history.AppendAskMessage(ctx, history.AskMessage{
		RunID:   runID,
		Role:    history.AskRoleAssistant,
		Content: "<think>checking</think>Costs are **high**.",
		Sources: []string{"notes/finance.md"},
		WebUsed: true,
	}); err != nil {
		t.Fatalf("AppendAskMessage assistant: %v", err)
	}
}

func TestAskPageRendersFollowupThreadAndForm(t *testing.T) {
	te := newTestEnv(t, webSearchChat(t), func(d *Deps) {
		d.WebSearchURL = "http://127.0.0.1:9"
	})
	saveFinishedAskRun(t, te, "run1", "What is revenue?", "Revenue is 10M.")
	seedFollowupThread(t, te, "run1")

	rr := getPage(t, te.server.Handler(), "/ask?run=run1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "render error") {
		t.Fatalf("ask page failed to render: %q", body)
	}
	for _, want := range []string{
		`id="followup-form"`,
		`id="followup-input"`,
		"What about costs?",
		"Costs are <strong>high</strong>",
		"внешний, непроверенный",
		"искать в интернете",
		`id="websearch-confirm"`,
		"checking",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ask page missing %q", want)
		}
	}
}

func TestAskPageHidesWebSearchToggleWhenDisabled(t *testing.T) {
	te := newTestEnv(t, &fakeChat{})
	saveFinishedAskRun(t, te, "run1", "What is revenue?", "Revenue is 10M.")
	seedFollowupThread(t, te, "run1")

	rr := getPage(t, te.server.Handler(), "/ask?run=run1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "искать в интернете") {
		t.Errorf("web search toggle present while feature disabled")
	}
	if strings.Contains(body, `id="websearch-confirm"`) {
		t.Errorf("web search confirmation dialog present while feature disabled")
	}
	if !strings.Contains(body, `id="followup-form"`) {
		t.Errorf("follow-up form missing while web search disabled")
	}
	if !strings.Contains(body, "What about costs?") {
		t.Errorf("follow-up thread missing: %q", body)
	}
}
