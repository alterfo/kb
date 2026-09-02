package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/store/vector"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kb.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesSchema(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, err := db.sql.ExecContext(ctx, `INSERT INTO chunks (id, ref_doc_id, text, file_path, file_name, source, token_count, chunk_index) VALUES ('a','doc','t','p','f','src',1,0)`); err != nil {
		t.Fatalf("insert into chunks: %v", err)
	}
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO kb_meta (key, value) VALUES ('k','v')`); err != nil {
		t.Fatalf("insert into kb_meta: %v", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	ctx := context.Background()

	db1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := db1.setMetaInt(ctx, "test_key", 7); err != nil {
		t.Fatalf("setMetaInt: %v", err)
	}
	db1.Close()

	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	v, ok, err := db2.getMetaInt(ctx, "test_key")
	if err != nil {
		t.Fatalf("getMetaInt: %v", err)
	}
	if !ok || v != 7 {
		t.Fatalf("got (%d,%v), want (7,true)", v, ok)
	}
}

func TestMetaIntRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, ok, err := db.getMetaInt(ctx, "missing"); err != nil || ok {
		t.Fatalf("getMetaInt(missing) = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	if err := db.setMetaInt(ctx, "dim", 384); err != nil {
		t.Fatalf("setMetaInt: %v", err)
	}
	v, ok, err := db.getMetaInt(ctx, "dim")
	if err != nil || !ok || v != 384 {
		t.Fatalf("getMetaInt(dim) = (%d,%v,%v), want (384,true,nil)", v, ok, err)
	}

	if err := db.setMetaInt(ctx, "dim", 768); err != nil {
		t.Fatalf("setMetaInt overwrite: %v", err)
	}
	v, ok, err = db.getMetaInt(ctx, "dim")
	if err != nil || !ok || v != 768 {
		t.Fatalf("getMetaInt(dim) after overwrite = (%d,%v,%v), want (768,true,nil)", v, ok, err)
	}
}

func TestCorpusVersionStartsAtZeroAndBumps(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	v, err := db.CorpusVersion(ctx)
	if err != nil || v != 0 {
		t.Fatalf("initial CorpusVersion = (%d,%v), want (0,nil)", v, err)
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		t.Fatalf("bumpCorpusVersion: %v", err)
	}
	if err := bumpCorpusVersion(ctx, tx); err != nil {
		t.Fatalf("bumpCorpusVersion 2: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	v, err = db.CorpusVersion(ctx)
	if err != nil || v != 2 {
		t.Fatalf("CorpusVersion after 2 bumps = (%d,%v), want (2,nil)", v, err)
	}
}

func TestEmbedDimReadsPersistedMeta(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if dim, ok, err := db.EmbedDim(ctx); err != nil || ok {
		t.Fatalf("EmbedDim(missing) = (%d,%v,%v), want (0,false,nil)", dim, ok, err)
	}

	if err := db.setMetaInt(ctx, metaKeyEmbedDim, 384); err != nil {
		t.Fatalf("setMetaInt: %v", err)
	}
	dim, ok, err := db.EmbedDim(ctx)
	if err != nil || !ok || dim != 384 {
		t.Fatalf("EmbedDim = (%d,%v,%v), want (384,true,nil)", dim, ok, err)
	}
}

func TestChunkCount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	n, err := db.ChunkCount(ctx)
	if err != nil || n != 0 {
		t.Fatalf("ChunkCount(empty) = (%d,%v), want (0,nil)", n, err)
	}

	vs := NewVectorStore(db)
	if err := vs.Upsert(ctx, []vector.Chunk{
		{ID: "a", RefDocID: "doc", Text: "one", FilePath: "p/a", FileName: "a", Source: "src", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc", Text: "two", FilePath: "p/b", FileName: "b", Source: "src", Embedding: []float32{0, 1}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	n, err = db.ChunkCount(ctx)
	if err != nil || n != 2 {
		t.Fatalf("ChunkCount = (%d,%v), want (2,nil)", n, err)
	}
}

func TestDeleteMeta(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := db.setMetaInt(ctx, metaKeyEmbedDim, 8); err != nil {
		t.Fatalf("setMetaInt: %v", err)
	}
	if err := db.deleteMeta(ctx, metaKeyEmbedDim); err != nil {
		t.Fatalf("deleteMeta: %v", err)
	}

	dim, ok, err := db.EmbedDim(ctx)
	if err != nil {
		t.Fatalf("EmbedDim: %v", err)
	}
	if ok || dim != 0 {
		t.Fatalf("after deleteMeta: ok=%v dim=%d, want (false, 0)", ok, dim)
	}

	if err := db.deleteMeta(ctx, metaKeyEmbedDim); err != nil {
		t.Fatalf("deleteMeta of missing key should be a no-op, got: %v", err)
	}
}

func TestGetMetaIntRejectsNonIntegerValue(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, err := db.sql.ExecContext(ctx, `INSERT INTO kb_meta (key, value) VALUES (?, ?)`, metaKeyEmbedDim, "not-an-int"); err != nil {
		t.Fatalf("seed bad meta: %v", err)
	}

	if _, _, err := db.EmbedDim(ctx); err == nil {
		t.Fatal("expected error for non-integer meta value")
	}
}

func TestOperationsOnClosedDBReturnErrors(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	gs := NewGraphStore(db)

	db.Close()

	if _, _, err := db.EmbedDim(ctx); err == nil {
		t.Fatal("EmbedDim on closed DB should fail")
	}
	if _, err := db.CorpusVersion(ctx); err == nil {
		t.Fatal("CorpusVersion on closed DB should fail")
	}
	if _, err := db.ChunkCount(ctx); err == nil {
		t.Fatal("ChunkCount on closed DB should fail")
	}
	if err := vs.Reembed(ctx); err == nil {
		t.Fatal("Reembed on closed DB should fail")
	}
	if err := vs.DeleteByDoc(ctx, "x"); err == nil {
		t.Fatal("DeleteByDoc on closed DB should fail")
	}
	if err := gs.PruneOrphans(ctx); err == nil {
		t.Fatal("PruneOrphans on closed DB should fail")
	}
}

func TestMigrateAddsChunkLifecycleColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		CREATE TABLE chunks (
			id TEXT PRIMARY KEY,
			ref_doc_id TEXT NOT NULL,
			text TEXT NOT NULL,
			file_path TEXT NOT NULL,
			file_name TEXT NOT NULL,
			source TEXT NOT NULL,
			token_count INTEGER NOT NULL,
			chunk_index INTEGER NOT NULL,
			embedding BLOB,
			metadata TEXT
		);
		CREATE TABLE relations (
			id TEXT PRIMARY KEY,
			src TEXT NOT NULL,
			dst TEXT NOT NULL,
			type TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			weight REAL NOT NULL DEFAULT 0,
			source_chunks TEXT NOT NULL DEFAULT '[]'
		);
	`); err != nil {
		raw.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO chunks (id, ref_doc_id, text, file_path, file_name, source, token_count, chunk_index)
		VALUES ('c1', 'doc1', 'hello', 'p', 'f', 'src', 1, 0)
	`); err != nil {
		raw.Close()
		t.Fatalf("seed legacy chunk: %v", err)
	}
	raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	defer db.Close()

	rows, err := db.sql.QueryContext(ctx, `PRAGMA table_info(chunks)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = true
	}
	rows.Close()
	for _, col := range []string{"created_at", "valid_to", "replaces", "superseded_by"} {
		if !cols[col] {
			t.Fatalf("migration did not add column %q", col)
		}
	}

	var created, validTo, replaces, superseded any
	if err := db.sql.QueryRowContext(ctx, `SELECT created_at, valid_to, replaces, superseded_by FROM chunks WHERE id = 'c1'`).Scan(&created, &validTo, &replaces, &superseded); err != nil {
		t.Fatalf("select lifecycle columns: %v", err)
	}
	if created != nil || validTo != nil || replaces != nil || superseded != nil {
		t.Fatalf("legacy chunk lifecycle columns = %v/%v/%v/%v, want all NULL", created, validTo, replaces, superseded)
	}

	var idxCount int
	if err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_index_list('chunks')
		WHERE name = 'idx_chunks_superseded_by'
	`).Scan(&idxCount); err != nil {
		t.Fatalf("check superseded index: %v", err)
	}
	if idxCount != 1 {
		t.Fatal("superseded index idx_chunks_superseded_by missing after migration")
	}
}

func TestMigrateChunksTemporalIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		CREATE TABLE chunks (
			id TEXT PRIMARY KEY,
			ref_doc_id TEXT NOT NULL,
			text TEXT NOT NULL,
			file_path TEXT NOT NULL,
			file_name TEXT NOT NULL,
			source TEXT NOT NULL,
			token_count INTEGER NOT NULL,
			chunk_index INTEGER NOT NULL,
			embedding BLOB,
			metadata TEXT
		);
		CREATE TABLE relations (
			id TEXT PRIMARY KEY,
			src TEXT NOT NULL,
			dst TEXT NOT NULL,
			type TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			weight REAL NOT NULL DEFAULT 0,
			source_chunks TEXT NOT NULL DEFAULT '[]'
		);
	`); err != nil {
		raw.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	raw.Close()

	db1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close()

	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	rows, err := db2.sql.QueryContext(ctx, `PRAGMA table_info(chunks)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = true
	}
	rows.Close()
	for _, col := range []string{"created_at", "valid_to", "replaces", "superseded_by"} {
		if !cols[col] {
			t.Fatalf("reopen lost column %q", col)
		}
	}
}

func TestOpenFailsWhenParentIsFile(t *testing.T) {
	ctx := context.Background()
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if _, err := Open(ctx, filepath.Join(parent, "kb.db")); err == nil {
		t.Fatal("Open under a file path should fail")
	}
}

func TestMigrateOnClosedDBFails(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	db.Close()

	if err := db.migrateRelationsTemporal(ctx); err == nil {
		t.Fatal("migrateRelationsTemporal on closed DB should fail")
	}
	if err := db.migrateChunksTemporal(ctx); err == nil {
		t.Fatal("migrateChunksTemporal on closed DB should fail")
	}
	if err := db.migrateCommunitiesStale(ctx); err == nil {
		t.Fatal("migrateCommunitiesStale on closed DB should fail")
	}
}

func TestBackupToCreatesUsableCopy(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO chunks (id, ref_doc_id, text, file_path, file_name, source, token_count, chunk_index) VALUES ('c1','doc1','hello','p','f','src',1,0)`); err != nil {
		t.Fatalf("seed chunk: %v", err)
	}
	if err := db.setMetaInt(ctx, metaKeyCorpusVersion, 4); err != nil {
		t.Fatalf("set meta: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "backups", "kb.db")
	if err := db.BackupTo(ctx, dest); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	copyDB, err := Open(ctx, dest)
	if err != nil {
		t.Fatalf("Open backup: %v", err)
	}
	defer copyDB.Close()

	n, err := copyDB.ChunkCount(ctx)
	if err != nil {
		t.Fatalf("ChunkCount backup: %v", err)
	}
	if n != 1 {
		t.Fatalf("backup chunk count = %d, want 1", n)
	}
	v, err := copyDB.CorpusVersion(ctx)
	if err != nil || v != 4 {
		t.Fatalf("backup corpus version = (%d,%v), want (4,nil)", v, err)
	}
}

func TestBackupToRefusesExistingDestination(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	dest := filepath.Join(t.TempDir(), "kb.db")
	if err := os.WriteFile(dest, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write dest: %v", err)
	}
	if err := db.BackupTo(ctx, dest); err == nil {
		t.Fatal("BackupTo over an existing file should fail")
	}
}

func TestIntegrityCheckReturnsOK(t *testing.T) {
	db := openTestDB(t)
	got, err := db.IntegrityCheck(context.Background())
	if err != nil {
		t.Fatalf("IntegrityCheck: %v", err)
	}
	if got != "ok" {
		t.Fatalf("IntegrityCheck = %q, want %q", got, "ok")
	}
}
