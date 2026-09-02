package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/history"
)

// scriptedWebChat returns a canned response keyed by the system-prompt
// prefix, mirroring the got package's scriptedChat so the ask flow can be
// driven end-to-end without a live LLM.
type scriptedWebChat struct {
	byPrompt map[string]string
	fallback string
}

func (s scriptedWebChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	sys := ""
	if len(req.Messages) > 0 {
		sys = req.Messages[0].Content
	}
	for prefix, content := range s.byPrompt {
		if strings.HasPrefix(sys, prefix) {
			return llm.ChatResponse{Content: content}, nil
		}
	}
	return llm.ChatResponse{Content: s.fallback}, nil
}

func startAskRun(t *testing.T, te *testEnv, q string) string {
	t.Helper()
	rr := postForm(t, te.server.Handler(), "/ask/start", url.Values{"q": {q}})
	if rr.Code != http.StatusFound {
		t.Fatalf("start status = %d, want 302 (body %s)", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	id := strings.TrimPrefix(loc, "/ask?run=")
	if id == loc {
		t.Fatalf("unexpected redirect location %q", loc)
	}
	return id
}

func waitAskDone(t *testing.T, te *testEnv, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rr := getPage(t, te.server.Handler(), "/ask/status?id="+id)
		if rr.Code == http.StatusOK && strings.Contains(rr.Body.String(), `"done":true`) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ask run did not finish in time")
}

func TestAsk_StartStatusDegrade(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/askdoc.md", doc("askdoc", "notes", "retrievable content for the ask flow"))
	te.index(t, "notes/askdoc.md")

	id := startAskRun(t, te, "what is retrievable?")
	waitAskDone(t, te, id)

	rr := getPage(t, te.server.Handler(), "/ask/status?id="+id)
	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"done":true`) {
		t.Errorf("status not done: %s", body)
	}
	if !strings.Contains(body, `"final_answer"`) {
		t.Errorf("status missing final answer: %s", body)
	}
	if !strings.Contains(body, `"contract_version":2`) {
		t.Errorf("status missing contract_version: %s", body)
	}
}

func TestAsk_UnknownRunRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/ask/status?id=bogus")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown run status = %d, want 400", rr.Code)
	}
}

func TestAsk_EmptyQuestionRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/ask/start", url.Values{"q": {"  "}})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty question status = %d, want 400", rr.Code)
	}
}

func TestAsk_SSEStreamsDoneEvent(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/ssedoc.md", doc("ssedoc", "notes", "sse retrievable content"))
	te.index(t, "notes/ssedoc.md")

	id := startAskRun(t, te, "sse question")
	waitAskDone(t, te, id)

	ts := httptest.NewServer(te.server.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/ask/events?id=" + id)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "event: done") {
		t.Errorf("SSE missing done event: %s", text)
	}
	if !strings.Contains(text, "final_answer") {
		t.Errorf("SSE missing final answer payload: %s", text)
	}
}

func TestAsk_SSEStreamsInFlightProgressAndDone(t *testing.T) {
	release := make(chan struct{})
	chat := &fakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
		<-release
		return llm.ChatResponse{}, nil
	}}
	te := newTestEnv(t, chat)
	writeDoc(t, te.root, "notes/sselive.md", doc("sselive", "notes", "live sse retrievable content"))
	te.index(t, "notes/sselive.md")

	id := startAskRun(t, te, "live sse question")

	req := httptest.NewRequest(http.MethodGet, "/ask/events?id="+id, nil)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		te.server.Handler().ServeHTTP(rr, req)
		close(done)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		te.server.asks.mu.Lock()
		r, ok := te.server.asks.runs[id]
		subs := 0
		if ok {
			subs = len(r.subs)
		}
		te.server.asks.mu.Unlock()
		if ok && subs == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("events handler never subscribed to the in-flight run")
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(release)

	select {
	case <-done:
		text := rr.Body.String()
		if !strings.Contains(text, "event: done") {
			t.Errorf("SSE stream missing done event: %s", text)
		}
		if !strings.Contains(text, "event: progress") {
			t.Errorf("SSE stream missing in-flight progress events: %s", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SSE stream did not terminate after the run finished")
	}
}

func TestAsk_PersistsRunToHistory(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/histdoc.md", doc("histdoc", "notes", "content for history persistence"))
	te.index(t, "notes/histdoc.md")

	id := startAskRun(t, te, "history question")
	waitAskDone(t, te, id)

	entry, ok, err := te.history.AskRun(context.Background(), id)
	if err != nil {
		t.Fatalf("AskRun: %v", err)
	}
	if !ok {
		t.Fatalf("AskRun: run %s not persisted", id)
	}
	if entry.Status != history.AskRunStatusDone {
		t.Errorf("persisted status = %q, want %q", entry.Status, history.AskRunStatusDone)
	}
	if entry.Query != "history question" {
		t.Errorf("persisted query = %q, want %q", entry.Query, "history question")
	}
	if entry.FinishedAt == nil {
		t.Errorf("persisted FinishedAt is nil for a done run")
	}
	if !strings.Contains(entry.GraphJSON, "final_answer") {
		t.Errorf("persisted GraphJSON missing final_answer: %q", entry.GraphJSON)
	}
}

func TestAsk_HistoryPageListsRuns(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/histpage.md", doc("histpage", "notes", "content for the history page"))
	te.index(t, "notes/histpage.md")

	id := startAskRun(t, te, "history page question")
	waitAskDone(t, te, id)

	rr := getPage(t, te.server.Handler(), "/ask/history")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "history page question") {
		t.Errorf("history page missing query: %q", body)
	}
	if !strings.Contains(body, "/ask?run="+id) {
		t.Errorf("history page missing link to run: %q", body)
	}
}

func TestAsk_HistoryPageEmpty(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/ask/history")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no ask runs yet") {
		t.Errorf("expected empty state, got %q", rr.Body.String())
	}
}

func TestAsk_PageEmbedsStructuredGraphForCompletedRun(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/structdoc.md", doc("structdoc", "notes", "content for structured rendering"))
	te.index(t, "notes/structdoc.md")

	id := startAskRun(t, te, "structured question")
	waitAskDone(t, te, id)

	rr := getPage(t, te.server.Handler(), "/ask?run="+id)
	body := rr.Body.String()
	if strings.Contains(body, "render error") {
		t.Errorf("ask page failed to render: %q", body)
	}
	if !strings.Contains(body, "renderGraph(") {
		t.Errorf("ask page missing renderGraph call: %q", body)
	}
	if !strings.Contains(body, `"final_answer"`) {
		t.Errorf("ask page missing embedded graph JSON: %q", body)
	}
	if strings.Contains(body, `id="ask-out">waiting for progress`) {
		t.Errorf("ask page should no longer server-render a static waiting placeholder: %q", body)
	}
	if !strings.Contains(body, "status: done") {
		t.Errorf("ask page missing status: %q", body)
	}
}

// TestAsk_PageFallsBackToHistoryAfterRestart simulates a server restart: a
// fresh Server sharing the same persisted store but a brand-new (empty)
// in-memory askManager must still be able to render a previously finished
// run from the history table instead of showing an empty page.
func TestAsk_PageFallsBackToHistoryAfterRestart(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/restartdoc.md", doc("restartdoc", "notes", "content surviving a restart"))
	te.index(t, "notes/restartdoc.md")

	id := startAskRun(t, te, "restart question")
	waitAskDone(t, te, id)

	restarted := NewServer(Deps{
		Root:        te.root,
		PersistDir:  te.persist,
		Vector:      te.vector,
		Graph:       te.graph,
		History:     te.history,
		SourcesPath: filepath.Join(te.root, "sources.yaml"),
	})

	rr := getPage(t, restarted.Handler(), "/ask?run="+id)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "restart question") {
		t.Errorf("restarted page missing query: %q", body)
	}
	if !strings.Contains(body, "status: done") {
		t.Errorf("restarted page missing persisted status: %q", body)
	}
	if !strings.Contains(body, `"final_answer"`) {
		t.Errorf("restarted page missing persisted graph: %q", body)
	}
}

// TestNewServer_MarksStaleRunningRunsInterrupted covers the startup
// recovery path directly: a run left "running" by a crashed/killed
// previous process must never show as permanently "running" in history.
func TestNewServer_MarksStaleRunningRunsInterrupted(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := te.history.SaveAskRun(ctx, history.AskRunEntry{
		ID: "stale-run", Query: "q", Status: history.AskRunStatusRunning,
		GraphJSON: "{}", CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAskRun: %v", err)
	}

	NewServer(Deps{
		Root:        te.root,
		PersistDir:  te.persist,
		Vector:      te.vector,
		Graph:       te.graph,
		History:     te.history,
		SourcesPath: filepath.Join(te.root, "sources.yaml"),
	})

	entry, ok, err := te.history.AskRun(ctx, "stale-run")
	if err != nil {
		t.Fatalf("AskRun: %v", err)
	}
	if !ok {
		t.Fatalf("AskRun: stale-run not found")
	}
	if entry.Status != history.AskRunStatusInterrupted {
		t.Errorf("status = %q, want %q", entry.Status, history.AskRunStatusInterrupted)
	}
}

func TestAskManagerEvictsOldestFinishedRuns(t *testing.T) {
	m := newAskManager(func(f func()) { f() })
	var ids []string
	for i := 0; i < maxAskRuns+2; i++ {
		ids = append(ids, m.start(fmt.Sprintf("q%d", i), func(id string) got.ThoughtGraph {
			return got.ThoughtGraph{Query: id}
		}))
	}

	m.mu.Lock()
	kept := len(m.runs)
	var evicted []string
	for _, id := range ids[:2] {
		if _, ok := m.runs[id]; !ok {
			evicted = append(evicted, id)
		}
	}
	var retained []string
	for _, id := range ids[2:] {
		if _, ok := m.runs[id]; !ok {
			retained = append(retained, id)
		}
	}
	m.mu.Unlock()

	if kept != maxAskRuns {
		t.Fatalf("runs kept = %d, want %d", kept, maxAskRuns)
	}
	if len(evicted) != 2 {
		t.Errorf("oldest finished runs not evicted: %v", ids[:2])
	}
	if len(retained) != 0 {
		t.Errorf("newest runs must be retained, missing: %v", retained)
	}
}

func TestAsk_ApproveWritesApprovedNote(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/appdoc.md", doc("appdoc", "notes", "content for approval flow"))
	te.index(t, "notes/appdoc.md")

	id := startAskRun(t, te, "approval question")
	waitAskDone(t, te, id)

	rr := postForm(t, te.server.Handler(), "/ask/approve", url.Values{
		"id":    {id},
		"title": {"Approved Title"},
	})
	if rr.Code != http.StatusFound {
		t.Fatalf("approve status = %d, want 302 (body %s)", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "/documents/view?path=notes/approved/") {
		t.Fatalf("approve redirect = %q", loc)
	}
	path := strings.TrimPrefix(loc, "/documents/view?path=")
	if _, err := os.Stat(filepath.Join(te.root, filepath.FromSlash(path))); err != nil {
		t.Errorf("approved note missing: %v", err)
	}
}

func TestAsk_ApproveDistinctSlugsForCollidingQueries(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/appdoc.md", doc("appdoc", "notes", "content for approval flow"))
	te.index(t, "notes/appdoc.md")

	prefix := strings.Repeat("q", 60)
	for _, q := range []string{prefix + " first", prefix + " second"} {
		id := startAskRun(t, te, q)
		waitAskDone(t, te, id)
		rr := postForm(t, te.server.Handler(), "/ask/approve", url.Values{"id": {id}})
		if rr.Code != http.StatusFound {
			t.Fatalf("approve status = %d, want 302 (body %s)", rr.Code, rr.Body.String())
		}
	}

	entries, err := os.ReadDir(filepath.Join(te.root, "notes", "approved"))
	if err != nil {
		t.Fatalf("read approved dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("approved notes = %d, want 2 distinct files for queries sharing the first 60 runes: %v", len(entries), entries)
	}
}

func TestAsk_ApproveSameQueryTwiceDoesNotOverwrite(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/appdoc.md", doc("appdoc", "notes", "content for approval flow"))
	te.index(t, "notes/appdoc.md")

	var paths []string
	for i := 0; i < 2; i++ {
		id := startAskRun(t, te, "approve twice question")
		waitAskDone(t, te, id)
		rr := postForm(t, te.server.Handler(), "/ask/approve", url.Values{"id": {id}})
		if rr.Code != http.StatusFound {
			t.Fatalf("approve #%d status = %d, want 302 (body %s)", i+1, rr.Code, rr.Body.String())
		}
		loc := rr.Header().Get("Location")
		path := strings.TrimPrefix(loc, "/documents/view?path=")
		if path == loc {
			t.Fatalf("approve #%d redirect = %q", i+1, loc)
		}
		paths = append(paths, path)
	}

	if paths[0] == paths[1] {
		t.Fatalf("second approval redirected to the same path %q, want a unique destination", paths[0])
	}
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(te.root, filepath.FromSlash(p))); err != nil {
			t.Errorf("approved note %s missing: %v", p, err)
		}
	}
}

func TestAsk_PromoteTombstonesCitedSource(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "github/promotedoc.md", doc("promotedoc", "github", "upstream content for promotion"))
	te.index(t, "github/promotedoc.md")

	id := startAskRun(t, te, "promotion question")
	waitAskDone(t, te, id)

	rr := postForm(t, te.server.Handler(), "/ask/promote", url.Values{"id": {id}})
	if rr.Code != http.StatusOK {
		t.Fatalf("promote status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "promoted") {
		t.Errorf("promote result missing message: %s", body)
	}
	tsData, err := os.ReadFile(filepath.Join(te.persist, ".tombstones.json"))
	if err != nil {
		t.Fatalf("reading tombstones: %v", err)
	}
	if !strings.Contains(string(tsData), `"github"`) || !strings.Contains(string(tsData), `"promotedoc"`) {
		t.Errorf("tombstone not recorded: %s", tsData)
	}
}

func TestAsk_PersistedRunRemainsDoneWithSynchronousSpawn(t *testing.T) {
	te := newTestEnv(t, nil)
	te.server.asks = newAskManager(func(f func()) { f() })
	writeDoc(t, te.root, "notes/syncdone.md", doc("syncdone", "notes", "retrievable content for sync spawn ask"))
	te.index(t, "notes/syncdone.md")

	id := startAskRun(t, te, "what is retrievable?")
	e, ok, err := te.history.AskRun(context.Background(), id)
	if err != nil {
		t.Fatalf("AskRun: %v", err)
	}
	if !ok {
		t.Fatalf("AskRun: run %q not persisted", id)
	}
	if e.Status != history.AskRunStatusDone {
		t.Fatalf("persisted status = %q, want %q", e.Status, history.AskRunStatusDone)
	}
	if e.FinishedAt == nil {
		t.Fatal("persisted run has no finished_at")
	}
}

func TestAsk_ReasoningDAGAndExpansionRendered(t *testing.T) {
	chat := scriptedWebChat{
		byPrompt: map[string]string{
			"You break a user question into 2-5 focused":     `[{"subquestion":"first fact","depends_on":[]},{"subquestion":"second fact","depends_on":[0]}]`,
			"You answer a focused sub-question":              "a synthesized sub-answer",
			"You judge whether the given excerpts":           `{"covered":false,"score":0.1}`,
			"You combine sub-answers into one coherent":      "draft answer",
			"Given the original question and a draft answer": `[{"subquestion":"missing follow-up","reported_by":0}]`,
		},
	}
	te := newTestEnv(t, chat)
	writeDoc(t, te.root, "notes/dagdoc.md", doc("dagdoc", "notes", "dag reasoning retrievable content"))
	te.index(t, "notes/dagdoc.md")

	id := startAskRun(t, te, "multi-hop reasoning question")
	waitAskDone(t, te, id)

	rr := getPage(t, te.server.Handler(), "/ask/status?id="+id)
	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rr.Code)
	}
	var view struct {
		Done  bool             `json:"done"`
		Graph got.ThoughtGraph `json:"graph"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if !view.Done {
		t.Errorf("done = false, want true")
	}

	var sawPlan, sawRefine, sawDeps bool
	for _, n := range view.Graph.Nodes {
		switch n.Type {
		case "plan":
			sawPlan = true
		case "refine_subgoal":
			sawRefine = true
		}
		if len(n.Deps) > 0 {
			sawDeps = true
		}
	}
	if !sawPlan {
		t.Errorf("missing plan node in graph: %s", rr.Body.String())
	}
	if !sawRefine {
		t.Errorf("missing refine_subgoal node (expansion did not run): %s", rr.Body.String())
	}
	if !sawDeps {
		t.Errorf("missing deps on subgoal nodes: %s", rr.Body.String())
	}
}
