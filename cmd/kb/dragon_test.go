package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/config"
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
