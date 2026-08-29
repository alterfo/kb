package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/governance"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/mcp"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
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

func newTestEnv(t *testing.T, chat ChatClient) *testEnv {
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

	srv := NewServer(Deps{
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
	})

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
