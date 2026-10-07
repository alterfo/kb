package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/store/history"
	"github.com/alterfo/kb/internal/websearch/generalizer"
	"github.com/alterfo/kb/internal/websearch/guard"
	"github.com/alterfo/kb/internal/websearch/searxng"
)

const (
	webSearchTokenTTL       = 10 * time.Minute
	webSearchMaxRequestBody = 1 << 20
)

var (
	errWebSearchDisabled     = errors.New("web search is disabled")
	errWebSearchInvalidToken = errors.New("invalid or expired web search confirmation token")
	errWebSearchThreadLimit  = errors.New("web search limit reached for this thread")
	errWebSearchMissingRunID = errors.New("run_id is required")
	errWebSearchMissingQuery = errors.New("question is required")
)

type webSearchGuardError struct {
	reasons []guard.Reason
}

func (e *webSearchGuardError) Error() string {
	return "web search query blocked: " + joinGuardReasons(e.reasons)
}

type webSearchToken struct {
	runID     string
	queryHash string
	createdAt time.Time
}

type webSearchTokenStore struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	entries map[string]webSearchToken
}

func newWebSearchTokenStore(now func() time.Time) *webSearchTokenStore {
	if now == nil {
		now = time.Now
	}
	return &webSearchTokenStore{now: now, ttl: webSearchTokenTTL, entries: map[string]webSearchToken{}}
}

func (s *webSearchTokenStore) issue(runID, query string) string {
	token := newWebSearchTokenID()
	s.mu.Lock()
	s.entries[token] = webSearchToken{runID: runID, queryHash: webSearchQueryHash(runID, query), createdAt: s.now()}
	s.mu.Unlock()
	return token
}

func (s *webSearchTokenStore) validate(runID, query, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	if !ok || entry.runID != runID || entry.queryHash != webSearchQueryHash(runID, query) {
		return errWebSearchInvalidToken
	}
	if s.now().Sub(entry.createdAt) > s.ttl {
		delete(s.entries, token)
		return errWebSearchInvalidToken
	}
	return nil
}

func (s *webSearchTokenStore) consume(runID, query, token string) {
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
}

func newWebSearchTokenID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return randID()
	}
	return hex.EncodeToString(b[:])
}

func webSearchQueryHash(runID, query string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + query))
	return hex.EncodeToString(sum[:])
}

type webSearchService struct {
	enabled      bool
	client       *searxng.Client
	guard        *guard.Guard
	generalizer  *generalizer.Generalizer
	tokens       *webSearchTokenStore
	maxResults   int
	maxPerThread int
	now          func() time.Time
	audit        func(runID, query string, at time.Time)

	mu        sync.Mutex
	perThread map[string]int
}

func newWebSearchService(deps Deps) *webSearchService {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	maxResults := deps.WebSearchMaxResults
	if maxResults <= 0 {
		maxResults = searxng.DefaultMaxResults
	}
	service := &webSearchService{
		client:       searxng.New(searxng.Config{BaseURL: deps.WebSearchURL, MaxResults: maxResults}),
		tokens:       newWebSearchTokenStore(now),
		maxResults:   maxResults,
		maxPerThread: deps.WebSearchMaxPerThread,
		now:          now,
		audit:        deps.WebSearchAudit,
		perThread:    map[string]int{},
	}
	if !service.client.Enabled() {
		return service
	}
	g := guard.New(guard.Options{Denylist: deps.WebSearchDenylist})
	service.guard = g
	service.generalizer = generalizer.New(g, deps.LLMModel)
	service.enabled = true
	return service
}

func (s *webSearchService) isEnabled() bool {
	return s != nil && s.enabled
}

func (s *webSearchService) confirm(ctx context.Context, runID, query, token string) ([]searxng.Result, error) {
	if !s.isEnabled() {
		return nil, errWebSearchDisabled
	}
	if err := s.tokens.validate(runID, query, token); err != nil {
		return nil, err
	}
	verdict, err := s.guard.Check(query)
	if err != nil {
		return nil, fmt.Errorf("web search guard: %w", err)
	}
	if !verdict.Allowed {
		return nil, &webSearchGuardError{reasons: verdict.Reasons}
	}
	if !s.acquireThread(runID) {
		return nil, errWebSearchThreadLimit
	}
	s.tokens.consume(runID, query, token)
	results, err := s.client.Search(ctx, query, s.maxResults)
	if err == nil && s.audit != nil {
		s.audit(runID, query, s.now())
	}
	return results, err
}

func (s *webSearchService) acquireThread(runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxPerThread <= 0 {
		return true
	}
	if s.perThread[runID] >= s.maxPerThread {
		return false
	}
	s.perThread[runID]++
	return true
}

type followupSubmitRequest struct {
	RunID    string
	Question string
}

type webSearchProposeRequest struct {
	RunID    string
	Question string
}

type webSearchConfirmRequest struct {
	RunID    string
	Query    string
	Token    string
	Question string
}

type followupView struct {
	Answer        string   `json:"answer"`
	AnswerHTML    string   `json:"answer_html"`
	ReasoningHTML string   `json:"reasoning_html,omitempty"`
	WebUsed       bool     `json:"web_used"`
	Sources       []string `json:"sources,omitempty"`
}

type webSearchProposalView struct {
	Query string `json:"query"`
	Token string `json:"token"`
}

type webSearchErrorView struct {
	Error   string   `json:"error"`
	Reasons []string `json:"reasons,omitempty"`
}

func (s *Server) handleAskFollowup(w http.ResponseWriter, r *http.Request) {
	req, err := decodeFollowupSubmit(r)
	if err != nil {
		writeWebSearchError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	answer, err := s.answerFollowup(r.Context(), req.RunID, req.Question, nil, "")
	if err != nil {
		writeWebSearchError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	s.appendFollowupMessages(r.Context(), req.RunID, req.Question, answer)
	writeJSON(w, http.StatusOK, newFollowupView(answer))
}

func (s *Server) handleAskWebSearchPropose(w http.ResponseWriter, r *http.Request) {
	req, err := decodeWebSearchPropose(r)
	if err != nil {
		writeWebSearchError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if !s.websearch.isEnabled() {
		writeWebSearchError(w, http.StatusConflict, errWebSearchDisabled.Error(), nil)
		return
	}
	base, err := s.followupBaseRequest(r.Context(), req.RunID)
	if err != nil {
		writeWebSearchError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	query, err := s.websearch.generalizer.Propose(r.Context(), s.deps.Chat, req.Question, buildFollowupContext(base))
	if err != nil {
		var blocked *generalizer.BlockedError
		if errors.As(err, &blocked) {
			writeWebSearchError(w, http.StatusUnprocessableEntity, err.Error(), blocked.Reasons)
			return
		}
		writeWebSearchError(w, http.StatusUnprocessableEntity, err.Error(), nil)
		return
	}
	verdict, err := s.websearch.guard.Check(query)
	if err != nil {
		writeWebSearchError(w, http.StatusUnprocessableEntity, err.Error(), nil)
		return
	}
	if !verdict.Allowed {
		writeWebSearchError(w, http.StatusUnprocessableEntity, "web search query blocked", verdict.Reasons)
		return
	}
	token := s.websearch.tokens.issue(req.RunID, query)
	writeJSON(w, http.StatusOK, webSearchProposalView{Query: query, Token: token})
}

func (s *Server) handleAskWebSearchConfirm(w http.ResponseWriter, r *http.Request) {
	req, err := decodeWebSearchConfirm(r)
	if err != nil {
		writeWebSearchError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if !s.websearch.isEnabled() {
		writeWebSearchError(w, http.StatusConflict, errWebSearchDisabled.Error(), nil)
		return
	}
	results, err := s.websearch.confirm(r.Context(), req.RunID, req.Query, req.Token)
	if err != nil {
		var blocked *webSearchGuardError
		if errors.As(err, &blocked) {
			writeWebSearchError(w, http.StatusUnprocessableEntity, err.Error(), blocked.reasons)
			return
		}
		status := http.StatusForbidden
		switch {
		case errors.Is(err, errWebSearchInvalidToken):
			status = http.StatusForbidden
		case errors.Is(err, errWebSearchThreadLimit):
			status = http.StatusTooManyRequests
		default:
			status = http.StatusBadGateway
		}
		writeWebSearchError(w, status, err.Error(), nil)
		return
	}
	question := req.Question
	if strings.TrimSpace(question) == "" {
		question = req.Query
	}
	answer, err := s.answerFollowup(r.Context(), req.RunID, question, results, "")
	if err != nil {
		writeWebSearchError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	s.appendFollowupMessages(r.Context(), req.RunID, question, answer)
	writeJSON(w, http.StatusOK, newFollowupView(answer))
}

func (s *Server) answerFollowup(ctx context.Context, runID, question string, web []searxng.Result, notice string) (followupAnswer, error) {
	if strings.TrimSpace(runID) == "" {
		return followupAnswer{}, errWebSearchMissingRunID
	}
	if strings.TrimSpace(question) == "" {
		return followupAnswer{}, errWebSearchMissingQuery
	}
	base, err := s.followupBaseRequest(ctx, runID)
	if err != nil {
		return followupAnswer{}, err
	}
	thread, _ := s.askThread(ctx, runID)
	req := base
	req.Question = question
	req.Thread = thread
	req.Web = web
	req.Notice = notice
	if s.followup == nil {
		return followupAnswer{}, errors.New("followup engine is not configured")
	}
	return s.followup.answer(ctx, req)
}

func (s *Server) followupBaseRequest(ctx context.Context, runID string) (followupRequest, error) {
	graph, _, ok := s.loadFinishedAsk(ctx, runID)
	if !ok {
		return followupRequest{}, fmt.Errorf("unknown or unfinished ask run: %s", runID)
	}
	return followupRequest{
		OriginalQuestion: graph.Query,
		FinalAnswer:      graph.FinalAnswer,
		Sources:          graph.Sources,
		ChunkSources:     graph.ChunkSources,
	}, nil
}

func (s *Server) loadFinishedAsk(ctx context.Context, runID string) (got.ThoughtGraph, string, bool) {
	if runID == "" {
		return got.ThoughtGraph{}, "", false
	}
	if graph, done, ok := s.asks.get(runID); ok && done && strings.TrimSpace(graph.FinalAnswer) != "" {
		return graph, history.AskRunStatusDone, true
	}
	if s.deps.History == nil {
		return got.ThoughtGraph{}, "", false
	}
	entry, ok, err := s.deps.History.AskRun(ctx, runID)
	if err != nil || !ok {
		return got.ThoughtGraph{}, "", false
	}
	var graph got.ThoughtGraph
	if err := json.Unmarshal([]byte(entry.GraphJSON), &graph); err != nil {
		return got.ThoughtGraph{}, "", false
	}
	if strings.TrimSpace(graph.FinalAnswer) == "" {
		return got.ThoughtGraph{}, "", false
	}
	return graph, entry.Status, true
}

func (s *Server) askThread(ctx context.Context, runID string) ([]history.AskMessage, error) {
	if s.deps.History == nil {
		return nil, nil
	}
	return s.deps.History.AskThread(ctx, runID)
}

func (s *Server) appendFollowupMessages(ctx context.Context, runID, question string, answer followupAnswer) {
	if s.deps.History == nil {
		return
	}
	now := s.deps.Now()
	_ = s.deps.History.AppendAskMessage(ctx, history.AskMessage{RunID: runID, Role: history.AskRoleUser, Content: question, CreatedAt: now})
	_ = s.deps.History.AppendAskMessage(ctx, history.AskMessage{RunID: runID, Role: history.AskRoleAssistant, Content: answer.Content, Sources: answer.Sources, WebUsed: answer.WebUsed, CreatedAt: now})
}

func decodeFollowupSubmit(r *http.Request) (followupSubmitRequest, error) {
	values, err := requestStringValues(r)
	if err != nil {
		return followupSubmitRequest{}, err
	}
	return followupSubmitRequest{RunID: strings.TrimSpace(values["run_id"]), Question: strings.TrimSpace(values["question"])}, nil
}

func decodeWebSearchPropose(r *http.Request) (webSearchProposeRequest, error) {
	values, err := requestStringValues(r)
	if err != nil {
		return webSearchProposeRequest{}, err
	}
	return webSearchProposeRequest{RunID: strings.TrimSpace(values["run_id"]), Question: strings.TrimSpace(values["question"])}, nil
}

func decodeWebSearchConfirm(r *http.Request) (webSearchConfirmRequest, error) {
	values, err := requestStringValues(r)
	if err != nil {
		return webSearchConfirmRequest{}, err
	}
	return webSearchConfirmRequest{
		RunID:    strings.TrimSpace(values["run_id"]),
		Query:    strings.TrimSpace(values["query"]),
		Token:    strings.TrimSpace(values["token"]),
		Question: strings.TrimSpace(values["question"]),
	}, nil
}

func requestStringValues(r *http.Request) (map[string]string, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		dec := json.NewDecoder(io.LimitReader(r.Body, webSearchMaxRequestBody))
		var raw map[string]string
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(r.Form))
	for key := range r.Form {
		values[key] = r.Form.Get(key)
	}
	return values, nil
}

func writeWebSearchError(w http.ResponseWriter, status int, message string, reasons []guard.Reason) {
	writeJSON(w, status, webSearchErrorView{Error: message, Reasons: guardReasonStrings(reasons)})
}

func guardReasonStrings(reasons []guard.Reason) []string {
	out := make([]string, len(reasons))
	for i, r := range reasons {
		out[i] = string(r)
	}
	return out
}

func joinGuardReasons(reasons []guard.Reason) string {
	return strings.Join(guardReasonStrings(reasons), ", ")
}

func newFollowupView(answer followupAnswer) followupView {
	return followupView{
		Answer:        answer.Content,
		AnswerHTML:    string(answer.AnswerHTML),
		ReasoningHTML: string(answer.ReasoningHTML),
		WebUsed:       answer.WebUsed,
		Sources:       answer.Sources,
	}
}
