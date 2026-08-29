package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/memdb"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS chunks (
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
CREATE INDEX IF NOT EXISTS idx_chunks_ref_doc_id ON chunks(ref_doc_id);
CREATE INDEX IF NOT EXISTS idx_chunks_source ON chunks(source);

CREATE TABLE IF NOT EXISTS kb_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS doc_hashes (
	ref_doc_id TEXT PRIMARY KEY,
	hash TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS entities (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	source_chunks TEXT NOT NULL DEFAULT '[]',
	degree INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(name);

CREATE TABLE IF NOT EXISTS relations (
	id TEXT PRIMARY KEY,
	src TEXT NOT NULL,
	dst TEXT NOT NULL,
	type TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	weight REAL NOT NULL DEFAULT 0,
	source_chunks TEXT NOT NULL DEFAULT '[]',
	valid_from TEXT,
	valid_to TEXT,
	created_at TEXT,
	expired_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_relations_src ON relations(src);
CREATE INDEX IF NOT EXISTS idx_relations_dst ON relations(dst);

CREATE TABLE IF NOT EXISTS communities (
	id TEXT PRIMARY KEY,
	level INTEGER NOT NULL DEFAULT 0,
	members TEXT NOT NULL DEFAULT '[]',
	summary TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	source_chunks TEXT NOT NULL DEFAULT '[]',
	stale INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS search_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	query TEXT NOT NULL,
	source_filter TEXT NOT NULL DEFAULT '',
	results_count INTEGER NOT NULL DEFAULT 0,
	answer TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_search_history_created_at ON search_history(created_at);

CREATE TABLE IF NOT EXISTS ask_runs (
	id TEXT PRIMARY KEY,
	query TEXT NOT NULL,
	status TEXT NOT NULL,
	graph_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_ask_runs_created_at ON ask_runs(created_at);
`

const (
	metaKeyEmbedDim      = "embed_dim"
	metaKeyCorpusVersion = "corpus_version"
)

type DB struct {
	sql *sql.DB
}

func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("sqlite: create persist dir for %q: %w", path, err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: enable foreign_keys: %w", err)
	}

	d := &DB{sql: db}
	if err := d.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("sqlite: migrate schema: %w", err)
	}
	if err := d.migrateRelationsTemporal(ctx); err != nil {
		return err
	}
	if err := d.migrateRelationsConfidence(ctx); err != nil {
		return err
	}
	if err := d.migrateChunksTemporal(ctx); err != nil {
		return err
	}
	if err := d.migrateCommunitiesStale(ctx); err != nil {
		return err
	}
	if err := d.migrateSearchHistoryAnswer(ctx); err != nil {
		return err
	}
	return nil
}

// migrateSearchHistoryAnswer adds the answer column to search_history tables
// created before answer snapshots existed, so old rows keep their metadata
// and re-run remains available.
func (d *DB) migrateSearchHistoryAnswer(ctx context.Context) error {
	cols, err := d.tableColumns(ctx, "search_history")
	if err != nil {
		return fmt.Errorf("sqlite: migrate search_history: %w", err)
	}
	if cols["answer"] {
		return nil
	}
	if _, err := d.sql.ExecContext(ctx, `ALTER TABLE search_history ADD COLUMN answer TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("sqlite: migrate search_history: add column answer: %w", err)
	}
	return nil
}

// migrateCommunitiesStale adds the stale flag to communities tables created
// before lazy community refresh existed, so affected components keep their
// rows until a batch refresh recomputes them.
func (d *DB) migrateCommunitiesStale(ctx context.Context) error {
	cols, err := d.tableColumns(ctx, "communities")
	if err != nil {
		return fmt.Errorf("sqlite: migrate communities: %w", err)
	}

	if !cols["stale"] {
		if _, err := d.sql.ExecContext(ctx, `ALTER TABLE communities ADD COLUMN stale INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("sqlite: migrate communities: add column stale: %w", err)
		}
	}
	return nil
}

func (d *DB) migrateChunksTemporal(ctx context.Context) error {
	cols, err := d.tableColumns(ctx, "chunks")
	if err != nil {
		return fmt.Errorf("sqlite: migrate chunks: %w", err)
	}

	for _, col := range []struct{ name, decl string }{
		{"created_at", "TEXT"},
		{"valid_to", "TEXT"},
		{"replaces", "TEXT"},
		{"superseded_by", "TEXT"},
	} {
		if cols[col.name] {
			continue
		}
		if _, err := d.sql.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE chunks ADD COLUMN %s %s`, col.name, col.decl)); err != nil {
			return fmt.Errorf("sqlite: migrate chunks: add column %s: %w", col.name, err)
		}
	}

	if _, err := d.sql.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_chunks_superseded_by
		ON chunks(superseded_by)
	`); err != nil {
		return fmt.Errorf("sqlite: migrate chunks: superseded index: %w", err)
	}
	return nil
}

// migrateRelationsTemporal adds the bi-temporal columns to relations tables
// created before they existed, then creates the temporal lookup index.
func (d *DB) migrateRelationsTemporal(ctx context.Context) error {
	cols, err := d.tableColumns(ctx, "relations")
	if err != nil {
		return fmt.Errorf("sqlite: migrate relations: %w", err)
	}

	for _, col := range []struct{ name, decl string }{
		{"valid_from", "TEXT"},
		{"valid_to", "TEXT"},
		{"created_at", "TEXT"},
		{"expired_at", "TEXT"},
	} {
		if cols[col.name] {
			continue
		}
		if _, err := d.sql.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE relations ADD COLUMN %s %s`, col.name, col.decl)); err != nil {
			return fmt.Errorf("sqlite: migrate relations: add column %s: %w", col.name, err)
		}
	}

	if _, err := d.sql.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_relations_temporal
		ON relations(src, dst, type, valid_from, valid_to)
	`); err != nil {
		return fmt.Errorf("sqlite: migrate relations: temporal index: %w", err)
	}
	return nil
}

func (d *DB) migrateRelationsConfidence(ctx context.Context) error {
	cols, err := d.tableColumns(ctx, "relations")
	if err != nil {
		return fmt.Errorf("sqlite: migrate relations confidence: %w", err)
	}

	for _, col := range []struct{ name, decl string }{
		{"confidence", "REAL NOT NULL DEFAULT 1.0"},
		{"provenance", "TEXT NOT NULL DEFAULT 'legacy'"},
		{"extractor_version", "TEXT NOT NULL DEFAULT ''"},
	} {
		if cols[col.name] {
			continue
		}
		if _, err := d.sql.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE relations ADD COLUMN %s %s`, col.name, col.decl)); err != nil {
			return fmt.Errorf("sqlite: migrate relations confidence: add column %s: %w", col.name, err)
		}
	}
	return nil
}

// tableColumns lists the column names of a table via PRAGMA table_info.
func (d *DB) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := d.sql.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return nil, err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return cols, nil
}

func (d *DB) Close() error {
	return d.sql.Close()
}

func (d *DB) CorpusVersion(ctx context.Context) (int, error) {
	v, ok, err := d.getMetaInt(ctx, metaKeyCorpusVersion)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	return v, nil
}

// EmbedDim returns the persisted embedding dimension and whether it was set.
func (d *DB) EmbedDim(ctx context.Context) (int, bool, error) {
	return d.getMetaInt(ctx, metaKeyEmbedDim)
}

// ChunkCount returns the total number of indexed chunks.
func (d *DB) ChunkCount(ctx context.Context) (int, error) {
	var n int
	err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count chunks: %w", err)
	}
	return n, nil
}

func (d *DB) getMetaInt(ctx context.Context, key string) (int, bool, error) {
	var value string
	err := d.sql.QueryRowContext(ctx, `SELECT value FROM kb_meta WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("sqlite: read meta %q: %w", key, err)
	}
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
		return 0, false, fmt.Errorf("sqlite: meta %q not an int: %q", key, value)
	}
	return n, true, nil
}

func (d *DB) setMetaInt(ctx context.Context, key string, value int) error {
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO kb_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, fmt.Sprintf("%d", value))
	if err != nil {
		return fmt.Errorf("sqlite: write meta %q: %w", key, err)
	}
	return nil
}

func (d *DB) deleteMeta(ctx context.Context, key string) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM kb_meta WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("sqlite: delete meta %q: %w", key, err)
	}
	return nil
}

func bumpCorpusVersion(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO kb_meta (key, value) VALUES (?, '1')
		ON CONFLICT(key) DO UPDATE SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)
	`, metaKeyCorpusVersion)
	if err != nil {
		return fmt.Errorf("sqlite: bump corpus_version: %w", err)
	}
	return nil
}
