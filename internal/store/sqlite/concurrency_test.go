package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/store/vector"
	"github.com/ncruces/go-sqlite3/driver"
)

func TestOpenUsesWALAndPool(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	var mode string
	if err := db.sql.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	if got := db.sql.Stats().MaxOpenConnections; got != defaultMaxOpenConns {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, defaultMaxOpenConns)
	}
}

func TestOpenAllowsConcurrentConnections(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	c1, err := db.sql.Conn(ctx)
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	defer c1.Close()

	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c2, err := db.sql.Conn(ctx2)
	if err != nil {
		t.Fatalf("second connection should be available: %v", err)
	}
	defer c2.Close()

	stats := db.sql.Stats()
	if stats.OpenConnections < 2 {
		t.Fatalf("OpenConnections = %d, want >= 2", stats.OpenConnections)
	}
	if stats.InUse < 2 {
		t.Fatalf("InUse = %d, want >= 2", stats.InUse)
	}
}

func TestWALReadersAreNotBlockedByWriter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}
	if err := vs.Upsert(ctx, []vector.Chunk{
		{ID: "a", RefDocID: "doc", Text: "one", FilePath: "p", FileName: "f", Source: "src", Embedding: []float32{1, 0}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	wc, err := db.sql.Conn(ctx)
	if err != nil {
		t.Fatalf("writer connection: %v", err)
	}
	defer wc.Close()

	tx, err := wc.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatalf("begin write tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO kb_meta (key, value) VALUES ('held', '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatalf("writer insert: %v", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	n, err := db.ChunkCount(readCtx)
	if err != nil {
		t.Fatalf("read while writer active: %v", err)
	}
	if n != 1 {
		t.Fatalf("ChunkCount = %d, want 1", n)
	}

	var value string
	err = db.sql.QueryRowContext(readCtx, `SELECT value FROM kb_meta WHERE key = 'held'`).Scan(&value)
	if err != sql.ErrNoRows {
		t.Fatalf("reader saw uncommitted write (err = %v)", err)
	}
}

func TestConcurrentReadWriteKeepsIntegrity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	if err := vs.EnsureDim(ctx, 4); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	const (
		writers       = 8
		docsPerWriter = 25
	)
	var wg sync.WaitGroup
	errCh := make(chan error, writers*2)

	for i := 0; i < writers; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < docsPerWriter; j++ {
				docID := fmt.Sprintf("doc-%d-%d", i, j)
				if err := vs.Upsert(ctx, []vector.Chunk{
					{ID: docID + "#0", RefDocID: docID, Text: fmt.Sprintf("text %d %d", i, j), FilePath: "p", FileName: "f", Source: "src", Embedding: []float32{float32(i % 4), 0, 0, 0}},
				}); err != nil {
					errCh <- err
					return
				}
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < docsPerWriter*2; j++ {
				if _, err := db.ChunkCount(ctx); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent operation: %v", err)
	}

	n, err := db.ChunkCount(ctx)
	if err != nil {
		t.Fatalf("ChunkCount: %v", err)
	}
	if n != writers*docsPerWriter {
		t.Fatalf("ChunkCount = %d, want %d", n, writers*docsPerWriter)
	}
	got, err := db.IntegrityCheck(ctx)
	if err != nil || got != "ok" {
		t.Fatalf("IntegrityCheck = (%q, %v), want (ok, nil)", got, err)
	}
}

func TestCrashRecoveryKeepsDatabaseConsistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	ctx := context.Background()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}
	if err := vs.Upsert(ctx, []vector.Chunk{
		{ID: "a", RefDocID: "doc", Text: "committed", FilePath: "p", FileName: "f", Source: "src", Embedding: []float32{1, 0}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	crash, err := driver.Open(path)
	if err != nil {
		t.Fatalf("open crash connection: %v", err)
	}
	tx, err := crash.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatalf("begin crash tx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chunks (id, ref_doc_id, text, file_path, file_name, source, token_count, chunk_index) VALUES ('uncommitted','doc2','x','p','f','src',1,0)`); err != nil {
		t.Fatalf("write crash tx: %v", err)
	}
	crash.Close()
	db.Close()

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.IntegrityCheck(ctx)
	if err != nil || got != "ok" {
		t.Fatalf("IntegrityCheck after crash = (%q, %v), want (ok, nil)", got, err)
	}
	n, err := reopened.ChunkCount(ctx)
	if err != nil {
		t.Fatalf("ChunkCount after crash: %v", err)
	}
	if n != 1 {
		t.Fatalf("ChunkCount after crash = %d, want 1", n)
	}
}
