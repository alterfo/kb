package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AskCacheStore persists Ask-response cache entries in the same SQLite
// database as the corpus. Entries are keyed by a stable hash of
// (query + corpus_version + config fingerprint) and carry the corpus
// version they were recorded under so stale entries can be pruned.
type AskCacheStore struct {
	db *DB
}

func NewAskCacheStore(db *DB) *AskCacheStore {
	return &AskCacheStore{db: db}
}

func (s *AskCacheStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var raw string
	err := s.db.sql.QueryRowContext(ctx, `SELECT graph_json FROM ask_cache WHERE cache_key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: ask_cache get: %w", err)
	}
	return []byte(raw), true, nil
}

func (s *AskCacheStore) Put(ctx context.Context, key string, corpusVersion int, value []byte) error {
	if _, err := s.db.sql.ExecContext(ctx, `
		INSERT INTO ask_cache (cache_key, corpus_version, graph_json, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			graph_json = excluded.graph_json,
			created_at = excluded.created_at
	`, key, corpusVersion, string(value), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("sqlite: ask_cache put: %w", err)
	}
	return nil
}

func (s *AskCacheStore) Invalidate(ctx context.Context) error {
	if _, err := s.db.sql.ExecContext(ctx, `DELETE FROM ask_cache`); err != nil {
		return fmt.Errorf("sqlite: ask_cache invalidate: %w", err)
	}
	return nil
}

func (s *AskCacheStore) DeleteStale(ctx context.Context, corpusVersion int) error {
	if _, err := s.db.sql.ExecContext(ctx, `DELETE FROM ask_cache WHERE corpus_version != ?`, corpusVersion); err != nil {
		return fmt.Errorf("sqlite: ask_cache delete stale: %w", err)
	}
	return nil
}
