package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

func openDragonTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kb.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedDragonTestChunks(t *testing.T, db *sqlite.DB) {
	t.Helper()
	vs := sqlite.NewVectorStore(db)
	if err := vs.Upsert(context.Background(), []vector.Chunk{
		{ID: "a", RefDocID: "doc", Text: "one", FilePath: "p/a", FileName: "a", Source: "src", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc", Text: "two", FilePath: "p/b", FileName: "b", Source: "src", Embedding: []float32{0, 1}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}

func TestBenchIsolatedEnvPersistDir(t *testing.T) {
	persistDir := filepath.Join(t.TempDir(), "persist")
	env, cleanup, err := benchIsolatedEnv(config.Env{}, persistDir)
	if err != nil {
		t.Fatalf("benchIsolatedEnv: %v", err)
	}
	defer cleanup()

	if _, err := os.Stat(persistDir); err != nil {
		t.Fatalf("persist dir not created: %v", err)
	}
	if env.PersistDir != persistDir {
		t.Errorf("PersistDir = %q, want %q", env.PersistDir, persistDir)
	}
	if env.KBRoot != persistDir {
		t.Errorf("KBRoot = %q, want %q", env.KBRoot, persistDir)
	}

	cleanup()
	if _, err := os.Stat(persistDir); err != nil {
		t.Errorf("persist dir removed by cleanup: %v", err)
	}
}

func TestBenchIsolatedEnvDefaultTempDir(t *testing.T) {
	env, cleanup, err := benchIsolatedEnv(config.Env{}, "")
	if err != nil {
		t.Fatalf("benchIsolatedEnv: %v", err)
	}
	if env.PersistDir == "" {
		t.Fatalf("PersistDir = %q, want a temp dir", env.PersistDir)
	}
	if _, err := os.Stat(env.PersistDir); err != nil {
		t.Fatalf("temp persist dir not created: %v", err)
	}

	cleanup()
	if _, err := os.Stat(env.PersistDir); !os.IsNotExist(err) {
		t.Errorf("temp persist dir still exists after cleanup: %v", err)
	}
}

func TestBenchDragonReuseIndexFreshDB(t *testing.T) {
	db := openDragonTestDB(t)
	var stdout bytes.Buffer
	reuse, err := benchDragonReuseIndex(context.Background(), db, "/persist", false, &stdout)
	if err != nil {
		t.Fatalf("benchDragonReuseIndex: %v", err)
	}
	if reuse {
		t.Errorf("reuse = true, want false for a fresh DB")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestBenchDragonReuseIndexSecondRun(t *testing.T) {
	db := openDragonTestDB(t)
	seedDragonTestChunks(t, db)

	var stdout bytes.Buffer
	reuse, err := benchDragonReuseIndex(context.Background(), db, "/persist", false, &stdout)
	if err != nil {
		t.Fatalf("benchDragonReuseIndex: %v", err)
	}
	if !reuse {
		t.Errorf("reuse = false, want true for an indexed DB")
	}
	if !strings.Contains(stdout.String(), "reusing persisted index at /persist (2 chunks)") {
		t.Errorf("stdout = %q, want reuse log", stdout.String())
	}
}

func TestBenchDragonReuseIndexForceReindex(t *testing.T) {
	db := openDragonTestDB(t)
	seedDragonTestChunks(t, db)

	var stdout bytes.Buffer
	reuse, err := benchDragonReuseIndex(context.Background(), db, "/persist", true, &stdout)
	if err != nil {
		t.Fatalf("benchDragonReuseIndex: %v", err)
	}
	if reuse {
		t.Errorf("reuse = true, want false with -force-reindex")
	}
	if strings.Contains(stdout.String(), "reusing persisted index") {
		t.Errorf("stdout = %q, want no reuse log", stdout.String())
	}
}

func TestBenchDragonReuseIndexNoPersistDir(t *testing.T) {
	db := openDragonTestDB(t)
	seedDragonTestChunks(t, db)

	var stdout bytes.Buffer
	reuse, err := benchDragonReuseIndex(context.Background(), db, "", false, &stdout)
	if err != nil {
		t.Fatalf("benchDragonReuseIndex: %v", err)
	}
	if reuse {
		t.Errorf("reuse = true, want false without -persist-dir")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

type decomposeCountingChat struct {
	chatCalls      int
	decomposeCalls int
	resp           string
	chatErr        error
}

func (c *decomposeCountingChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	c.chatCalls++
	if c.chatErr != nil {
		return llm.ChatResponse{}, c.chatErr
	}
	hay := strings.ToLower(messageHaystackForTest(req.Messages))
	if strings.Contains(hay, "break a user question") {
		c.decomposeCalls++
		return llm.ChatResponse{Content: "[]", FinishReason: "stop"}, nil
	}
	return llm.ChatResponse{Content: c.resp, FinishReason: "stop"}, nil
}

func messageHaystackForTest(messages []llm.ChatMessage) string {
	contents := make([]string, len(messages))
	for i, m := range messages {
		contents[i] = m.Content
	}
	return strings.Join(contents, "\n")
}

func TestBenchDragonAskNaiveSkipsOrchestrator(t *testing.T) {
	db := openDragonTestDB(t)
	vs := sqlite.NewVectorStore(db)
	r := retriever.New(retriever.Config{Vector: vs})

	chat := &decomposeCountingChat{resp: "naive answer"}
	ask := benchDragonAsk(config.Env{LLMModel: "test-model"}, r, chat, "naive", 5)

	answer, docIDs := ask(context.Background(), corpus.Question{ID: "1", Text: "what is kb"})

	if answer != "naive answer" {
		t.Errorf("answer = %q, want %q", answer, "naive answer")
	}
	if chat.chatCalls != 1 {
		t.Errorf("chat calls = %d, want 1", chat.chatCalls)
	}
	if chat.decomposeCalls != 0 {
		t.Errorf("decompose calls = %d, want 0 (got.Orchestrator was constructed)", chat.decomposeCalls)
	}
	if len(docIDs) != 0 {
		t.Errorf("docIDs = %v, want empty (no retrieval legs configured)", docIDs)
	}
}

func TestBenchDragonGotConfigWiresContradictionDetection(t *testing.T) {
	chat := &decomposeCountingChat{resp: "x"}

	on := benchDragonGotConfig(config.Env{LLMModel: "m", DetectContradictions: true}, nil, chat, 5)
	if !on.DetectContradictions {
		t.Fatal("DetectContradictions = false, want true")
	}
	if on.ContradictionDetector == nil {
		t.Fatal("ContradictionDetector = nil, want non-nil")
	}

	off := benchDragonGotConfig(config.Env{LLMModel: "m"}, nil, chat, 5)
	if off.DetectContradictions {
		t.Fatal("DetectContradictions = true, want false when env unset")
	}
	if off.ContradictionDetector == nil {
		t.Fatal("ContradictionDetector = nil, want non-nil even when disabled")
	}
}

func TestBenchDragonGotConfigWiresMaxRefineLatencyMS(t *testing.T) {
	chat := &decomposeCountingChat{resp: "x"}

	cfg := benchDragonGotConfig(config.Env{LLMModel: "m", GoTMaxRefineLatencyMS: 90000}, nil, chat, 5)
	if cfg.MaxRefineLatencyMS != 90000 {
		t.Fatalf("MaxRefineLatencyMS = %d, want 90000", cfg.MaxRefineLatencyMS)
	}

	unset := benchDragonGotConfig(config.Env{LLMModel: "m"}, nil, chat, 5)
	if unset.MaxRefineLatencyMS != 0 {
		t.Fatalf("MaxRefineLatencyMS = %d, want 0 when env unset", unset.MaxRefineLatencyMS)
	}
}
