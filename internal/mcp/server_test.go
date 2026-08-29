package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

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

// testServer builds a Server against a real (temp-dir) SQLite store and a
// real in-memory BM25 index, with the LLM and knowledge graph left
// optional per test. It returns the Server plus its root dir and Indexer
// so tests can seed documents.
type testEnv struct {
	server  *Server
	root    string
	db      *sqlite.DB
	vector  vector.Store
	bm25    *bm25.Index
	graph   graphstore.Store
	indexer *engine.Indexer
}

func newTestEnv(t *testing.T, chat ChatClient) *testEnv {
	t.Helper()
	root := t.TempDir()
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	idx := engine.NewIndexer(engine.Config{Root: root, Vector: vs, ChunkSize: 512})
	bmIdx := bm25.New()

	deps := Deps{
		Root:        root,
		Vector:      vs,
		Versioner:   db,
		BM25:        bmIdx,
		Graph:       gs,
		Indexer:     idx,
		Chat:        chat,
		LLMModel:    "test-model",
		Hybrid:      true,
		RRFK:        60,
		DefaultK:    10,
		SourcesPath: filepath.Join(root, "sources.yaml"),
	}

	return &testEnv{
		server:  NewServer(deps),
		root:    root,
		db:      db,
		vector:  vs,
		bm25:    bmIdx,
		graph:   gs,
		indexer: idx,
	}
}

type fakeChat struct {
	resp llm.ChatResponse
	err  error
}

func (f *fakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return f.resp, f.err
}
