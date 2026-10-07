package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/history"
)

func webSearchChat(t *testing.T) *fakeChat {
	t.Helper()
	return &fakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
		for _, msg := range req.Messages {
			if strings.Contains(msg.Content, "You rewrite a user's follow-up question") {
				return llm.ChatResponse{Content: "regional revenue by country"}, nil
			}
		}
		for _, msg := range req.Messages {
			if strings.Contains(msg.Content, webResultsOpen) {
				return llm.ChatResponse{Content: "<think>checking</think>external answer [web:1]"}, nil
			}
		}
		return llm.ChatResponse{Content: "corpus answer"}, nil
	}}
}

func newWebSearchEnv(t *testing.T, searchURL string, opts ...testEnvOption) *testEnv {
	t.Helper()
	base := append([]testEnvOption{func(d *Deps) {
		d.WebSearchURL = searchURL
		d.WebSearchMaxResults = 5
	}}, opts...)
	return newTestEnv(t, webSearchChat(t), base...)
}

func saveFinishedAskRun(t *testing.T, te *testEnv, id, query, answer string) {
	t.Helper()
	graph := got.ThoughtGraph{
		Query:       query,
		FinalAnswer: answer,
		Sources:     []got.Source{{FileName: "finance.md", FilePath: "notes/finance.md"}},
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal graph: %v", err)
	}
	if err := te.history.SaveAskRun(context.Background(), history.AskRunEntry{
		ID:        id,
		Query:     query,
		Status:    history.AskRunStatusDone,
		GraphJSON: string(raw),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveAskRun: %v", err)
	}
}

func TestWebSearchProposeConfirmAnswerFlow(t *testing.T) {
	var searchCalls int
	var auditCalls int
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchCalls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"Revenue","url":"https://example.com/r","content":"regional data"}]}`))
	}))
	defer searchSrv.Close()

	te := newWebSearchEnv(t, searchSrv.URL, func(d *Deps) {
		d.WebSearchAudit = func(runID, query string, at time.Time) {
			auditCalls++
			if runID != "run1" || query == "" {
				t.Errorf("audit(%q, %q)", runID, query)
			}
		}
	})
	saveFinishedAskRun(t, te, "run1", "What is revenue?", "Revenue is 10M.")

	proposalRR := postJSON(t, te.server.Handler(), "/ask/followup/websearch/propose", map[string]string{
		"run_id":   "run1",
		"question": "How is revenue split by region?",
	})
	if proposalRR.Code != http.StatusOK {
		t.Fatalf("propose status = %d, body = %s", proposalRR.Code, proposalRR.Body.String())
	}
	if searchCalls != 0 {
		t.Fatalf("propose made %d web requests, want 0", searchCalls)
	}
	var proposal webSearchProposalView
	if err := json.Unmarshal(proposalRR.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	if proposal.Query == "" || proposal.Token == "" {
		t.Fatalf("proposal missing query/token: %+v", proposal)
	}

	confirmRR := postJSON(t, te.server.Handler(), "/ask/followup/websearch/confirm", map[string]string{
		"run_id":   "run1",
		"query":    proposal.Query,
		"token":    proposal.Token,
		"question": "How is revenue split by region?",
	})
	if confirmRR.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", confirmRR.Code, confirmRR.Body.String())
	}
	if searchCalls != 1 {
		t.Fatalf("confirm made %d web requests, want 1", searchCalls)
	}
	if auditCalls != 1 {
		t.Fatalf("audit calls = %d, want 1", auditCalls)
	}
	var view followupView
	if err := json.Unmarshal(confirmRR.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode answer: %v", err)
	}
	if !view.WebUsed || !strings.Contains(view.Answer, "external answer") {
		t.Fatalf("answer = %+v, want web-used external answer", view)
	}
	thread, err := te.history.AskThread(context.Background(), "run1")
	if err != nil {
		t.Fatalf("AskThread: %v", err)
	}
	if len(thread) != 2 || thread[0].Role != history.AskRoleUser || thread[1].Role != history.AskRoleAssistant || !thread[1].WebUsed {
		t.Fatalf("thread not persisted correctly: %+v", thread)
	}
}

func TestWebSearchConfirmWithoutTokenDoesNotSearch(t *testing.T) {
	var searchCalls int
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchCalls++
	}))
	defer searchSrv.Close()

	te := newWebSearchEnv(t, searchSrv.URL)
	saveFinishedAskRun(t, te, "run1", "q", "answer")

	rr := postJSON(t, te.server.Handler(), "/ask/followup/websearch/confirm", map[string]string{
		"run_id": "run1",
		"query":  "regional revenue",
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rr.Code, rr.Body.String())
	}
	if searchCalls != 0 {
		t.Fatalf("web requests = %d, want 0", searchCalls)
	}
}

func TestWebSearchConfirmModifiedQueryDoesNotSearch(t *testing.T) {
	var searchCalls int
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchCalls++
	}))
	defer searchSrv.Close()

	te := newWebSearchEnv(t, searchSrv.URL)
	saveFinishedAskRun(t, te, "run1", "q", "answer")

	proposalRR := postJSON(t, te.server.Handler(), "/ask/followup/websearch/propose", map[string]string{
		"run_id":   "run1",
		"question": "How is revenue split by region?",
	})
	var proposal webSearchProposalView
	if err := json.Unmarshal(proposalRR.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}

	rr := postJSON(t, te.server.Handler(), "/ask/followup/websearch/confirm", map[string]string{
		"run_id": "run1",
		"query":  proposal.Query + " modified",
		"token":  proposal.Token,
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rr.Code, rr.Body.String())
	}
	if searchCalls != 0 {
		t.Fatalf("web requests = %d, want 0", searchCalls)
	}
}

func TestWebSearchConfirmBlocksSecretAtConfirm(t *testing.T) {
	var searchCalls int
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchCalls++
	}))
	defer searchSrv.Close()

	te := newWebSearchEnv(t, searchSrv.URL)
	saveFinishedAskRun(t, te, "run1", "q", "answer")
	secretQuery := "find sk-abcdefghijklmnopqrstuvwxyz"
	token := te.server.websearch.tokens.issue("run1", secretQuery)

	rr := postJSON(t, te.server.Handler(), "/ask/followup/websearch/confirm", map[string]string{
		"run_id": "run1",
		"query":  secretQuery,
		"token":  token,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rr.Code, rr.Body.String())
	}
	if searchCalls != 0 {
		t.Fatalf("web requests = %d, want 0", searchCalls)
	}
	var errView webSearchErrorView
	if err := json.Unmarshal(rr.Body.Bytes(), &errView); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(errView.Reasons) == 0 || !containsString(errView.Reasons, "secret_key") {
		t.Fatalf("reasons = %v, want secret_key", errView.Reasons)
	}
}

func TestWebSearchDisabled(t *testing.T) {
	te := newTestEnv(t, webSearchChat(t))
	rr := postJSON(t, te.server.Handler(), "/ask/followup/websearch/propose", map[string]string{
		"run_id":   "run1",
		"question": "anything",
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rr.Code, rr.Body.String())
	}
}

func TestAskFollowupCorpusAnswer(t *testing.T) {
	te := newTestEnv(t, webSearchChat(t))
	saveFinishedAskRun(t, te, "run1", "What is revenue?", "Revenue is 10M.")

	rr := postJSON(t, te.server.Handler(), "/ask/followup", map[string]string{
		"run_id":   "run1",
		"question": "What about costs?",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var view followupView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode answer: %v", err)
	}
	if view.WebUsed || !strings.Contains(view.Answer, "corpus answer") {
		t.Fatalf("answer = %+v, want corpus answer", view)
	}
	thread, err := te.history.AskThread(context.Background(), "run1")
	if err != nil {
		t.Fatalf("AskThread: %v", err)
	}
	if len(thread) != 2 || thread[0].Content != "What about costs?" || thread[1].Content != "corpus answer" {
		t.Fatalf("thread = %+v", thread)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
