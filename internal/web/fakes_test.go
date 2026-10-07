package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/governance"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/mcp"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/history"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

type fakeChat struct {
	resp llm.ChatResponse
	err  error
	fn   func(req llm.ChatRequest) (llm.ChatResponse, error)
}

func (f *fakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if f.fn != nil {
		return f.fn(req)
	}
	return f.resp, f.err
}

type testEnv struct {
	server  *Server
	root    string
	persist string
	db      *sqlite.DB
	vector  vector.Store
	bm25    *bm25.Index
	graph   graphstore.Store
	history *sqlite.HistoryStore
	indexer *engine.Indexer
	gov     *governance.Governance
	chat    ChatClient
}

type testEnvOption func(*Deps)

func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func writeDoc(t *testing.T, root, relPath string, d connector.Document) {
	t.Helper()
	data, err := render.Render(d)
	if err != nil {
		t.Fatalf("render.Render: %v", err)
	}
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func newTestEnv(t *testing.T, chat ChatClient, opts ...testEnvOption) *testEnv {
	t.Helper()
	root := t.TempDir()
	persist := filepath.Join(root, ".persist")
	if err := os.MkdirAll(persist, 0o755); err != nil {
		t.Fatalf("mkdir persist: %v", err)
	}
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	hs := sqlite.NewHistoryStore(db)
	idx := engine.NewIndexer(engine.Config{Root: root, Vector: vs, ChunkSize: 512})
	bmIdx := bm25.New()
	gov := governance.New(root, idx, chat, "test-model")
	updater := graph.NewGraphUpdater(gs, nil, nil)
	mcpSrv := mcp.NewServer(mcp.Deps{
		Root:        root,
		Vector:      vs,
		Versioner:   db,
		BM25:        bmIdx,
		Graph:       gs,
		Indexer:     idx,
		Chat:        chat,
		LLMModel:    "test-model",
		SourcesPath: filepath.Join(root, "sources.yaml"),
	})

	deps := Deps{
		Root:         root,
		PersistDir:   persist,
		Vector:       vs,
		Versioner:    db,
		BM25:         bmIdx,
		Graph:        gs,
		GraphUpdater: updater,
		Indexer:      idx,
		History:      hs,
		MCP:          mcpSrv,
		Chat:         chat,
		LLMModel:     "test-model",
		Hybrid:       true,
		RRFK:         60,
		DefaultK:     10,
		SourcesPath:  filepath.Join(root, "sources.yaml"),
		Governance:   gov,
	}
	for _, opt := range opts {
		opt(&deps)
	}
	srv := NewServer(deps)

	return &testEnv{
		server:  srv,
		root:    root,
		persist: persist,
		db:      db,
		vector:  vs,
		bm25:    bmIdx,
		graph:   gs,
		history: hs,
		indexer: idx,
		gov:     gov,
		chat:    chat,
	}
}

func (te *testEnv) index(t *testing.T, relPath string) {
	t.Helper()
	if err := te.indexer.AddOrUpdateDocument(context.Background(), relPath); err != nil {
		t.Fatalf("AddOrUpdateDocument(%s): %v", relPath, err)
	}
}

func getPage(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func postForm(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

type fakeHistoryStore struct {
	runs    map[string]history.AskRunEntry
	threads map[string][]history.AskMessage
}

func newFakeHistoryStore() *fakeHistoryStore {
	return &fakeHistoryStore{
		runs:    map[string]history.AskRunEntry{},
		threads: map[string][]history.AskMessage{},
	}
}

var _ history.Store = (*fakeHistoryStore)(nil)

func (f *fakeHistoryStore) RecordSearch(context.Context, string, string, int, string, time.Duration, time.Time, ...string) error {
	return nil
}

func (f *fakeHistoryStore) SearchHistory(context.Context, int) ([]history.SearchEntry, error) {
	return nil, nil
}

func (f *fakeHistoryStore) SearchEntryByID(context.Context, int64) (history.SearchEntry, bool, error) {
	return history.SearchEntry{}, false, nil
}

func (f *fakeHistoryStore) RecordFeedback(context.Context, int64, int, time.Time) error {
	return nil
}

func (f *fakeHistoryStore) FeedbackByDoc(context.Context) (map[string]float64, error) {
	return nil, nil
}

func (f *fakeHistoryStore) LabeledEval(context.Context) ([]history.LabeledExample, error) {
	return nil, nil
}

func (f *fakeHistoryStore) SaveAskRun(_ context.Context, e history.AskRunEntry) error {
	f.runs[e.ID] = e
	return nil
}

func (f *fakeHistoryStore) AskRuns(context.Context, int) ([]history.AskRunEntry, error) {
	out := make([]history.AskRunEntry, 0, len(f.runs))
	for _, e := range f.runs {
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeHistoryStore) AskRun(_ context.Context, id string) (history.AskRunEntry, bool, error) {
	e, ok := f.runs[id]
	return e, ok, nil
}

func (f *fakeHistoryStore) MarkRunningInterrupted(context.Context) (int, error) {
	n := 0
	for id, e := range f.runs {
		if e.Status == history.AskRunStatusRunning {
			e.Status = history.AskRunStatusInterrupted
			f.runs[id] = e
			n++
		}
	}
	return n, nil
}

func (f *fakeHistoryStore) AppendAskMessage(_ context.Context, m history.AskMessage) error {
	if m.RunID == "" {
		return errors.New("fakeHistoryStore: run_id is required")
	}
	m.Seq = len(f.threads[m.RunID]) + 1
	m.Sources = append([]string(nil), m.Sources...)
	f.threads[m.RunID] = append(f.threads[m.RunID], m)
	return nil
}

func (f *fakeHistoryStore) AskThread(_ context.Context, runID string) ([]history.AskMessage, error) {
	out := make([]history.AskMessage, len(f.threads[runID]))
	copy(out, f.threads[runID])
	return out, nil
}

func TestFakeHistoryStoreAskThread(t *testing.T) {
	f := newFakeHistoryStore()
	ctx := context.Background()

	if err := f.AppendAskMessage(ctx, history.AskMessage{RunID: "r", Role: history.AskRoleUser, Content: "question"}); err != nil {
		t.Fatalf("AppendAskMessage user: %v", err)
	}
	if err := f.AppendAskMessage(ctx, history.AskMessage{RunID: "r", Role: history.AskRoleAssistant, Content: "answer", Sources: []string{"a.md"}, WebUsed: true}); err != nil {
		t.Fatalf("AppendAskMessage assistant: %v", err)
	}

	got, err := f.AskThread(ctx, "r")
	if err != nil {
		t.Fatalf("AskThread: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("AskThread: got %d messages, want 2", len(got))
	}
	if got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("AskThread: seq = %d,%d, want 1,2", got[0].Seq, got[1].Seq)
	}
	if got[1].WebUsed != true || len(got[1].Sources) != 1 || got[1].Sources[0] != "a.md" {
		t.Fatalf("AskThread[1]: unexpected fields: %+v", got[1])
	}
	if err := f.AppendAskMessage(ctx, history.AskMessage{Role: history.AskRoleUser, Content: "no run"}); err == nil {
		t.Fatalf("AppendAskMessage: expected error for missing run_id")
	}
}
