package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/alterfo/kb/internal/store/history"
)

// HistoryStore persists search queries and ask runs so the dashboard can
// show a spinner/history without depending on in-memory state that is lost
// on restart.
type HistoryStore struct {
	db *DB
}

func NewHistoryStore(db *DB) *HistoryStore {
	return &HistoryStore{db: db}
}

var _ history.Store = (*HistoryStore)(nil)

func (s *HistoryStore) RecordSearch(ctx context.Context, query, sourceFilter string, resultsCount int, answer string, duration time.Duration, at time.Time) error {
	if _, err := s.db.sql.ExecContext(ctx, `
		INSERT INTO search_history (query, source_filter, results_count, answer, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, query, sourceFilter, resultsCount, answer, duration.Milliseconds(), encodeTime(&at)); err != nil {
		return fmt.Errorf("sqlite: RecordSearch: %w", err)
	}
	return nil
}

func (s *HistoryStore) SearchHistory(ctx context.Context, limit int) ([]history.SearchEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT id, query, source_filter, results_count, answer, duration_ms, created_at
		FROM search_history ORDER BY created_at DESC, id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: SearchHistory: %w", err)
	}
	defer rows.Close()

	var out []history.SearchEntry
	for rows.Next() {
		e, err := scanSearchEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: SearchHistory: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: SearchHistory: %w", err)
	}
	return out, nil
}

func (s *HistoryStore) SearchEntryByID(ctx context.Context, id int64) (history.SearchEntry, bool, error) {
	row := s.db.sql.QueryRowContext(ctx, `
		SELECT id, query, source_filter, results_count, answer, duration_ms, created_at
		FROM search_history WHERE id = ?
	`, id)
	e, err := scanSearchEntry(row)
	if err == sql.ErrNoRows {
		return history.SearchEntry{}, false, nil
	}
	if err != nil {
		return history.SearchEntry{}, false, fmt.Errorf("sqlite: SearchEntryByID: %w", err)
	}
	return e, true, nil
}

// SaveAskRun upserts a run snapshot; callers pass the full row on every
// call (create and every progress/finish update) since ask runs are small
// and infrequent enough that read-modify-write isn't worth the complexity.
func (s *HistoryStore) SaveAskRun(ctx context.Context, e history.AskRunEntry) error {
	if e.ID == "" {
		return fmt.Errorf("sqlite: SaveAskRun: id is required")
	}
	if _, err := s.db.sql.ExecContext(ctx, `
		INSERT INTO ask_runs (id, query, status, graph_json, created_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			graph_json = excluded.graph_json,
			finished_at = excluded.finished_at
	`, e.ID, e.Query, e.Status, e.GraphJSON, encodeTime(&e.CreatedAt), encodeTime(e.FinishedAt)); err != nil {
		return fmt.Errorf("sqlite: SaveAskRun: %w", err)
	}
	return nil
}

func (s *HistoryStore) AskRuns(ctx context.Context, limit int) ([]history.AskRunEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT id, query, status, graph_json, created_at, finished_at
		FROM ask_runs ORDER BY created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: AskRuns: %w", err)
	}
	defer rows.Close()

	var out []history.AskRunEntry
	for rows.Next() {
		e, err := scanAskRun(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: AskRuns: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: AskRuns: %w", err)
	}
	return out, nil
}

func (s *HistoryStore) AskRun(ctx context.Context, id string) (history.AskRunEntry, bool, error) {
	row := s.db.sql.QueryRowContext(ctx, `
		SELECT id, query, status, graph_json, created_at, finished_at
		FROM ask_runs WHERE id = ?
	`, id)
	e, err := scanAskRun(row)
	if err == sql.ErrNoRows {
		return history.AskRunEntry{}, false, nil
	}
	if err != nil {
		return history.AskRunEntry{}, false, fmt.Errorf("sqlite: AskRun: %w", err)
	}
	return e, true, nil
}

// MarkRunningInterrupted flips every run still "running" to "interrupted".
// It is meant to be called once at server startup: a run whose goroutine
// died with the previous process can never finish, so leaving it "running"
// forever in the history would be misleading.
func (s *HistoryStore) MarkRunningInterrupted(ctx context.Context) (int, error) {
	res, err := s.db.sql.ExecContext(ctx, `
		UPDATE ask_runs SET status = ? WHERE status = ?
	`, history.AskRunStatusInterrupted, history.AskRunStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("sqlite: MarkRunningInterrupted: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: MarkRunningInterrupted: %w", err)
	}
	return int(n), nil
}

func scanSearchEntry(row scanner) (history.SearchEntry, error) {
	var e history.SearchEntry
	var createdAt string
	if err := row.Scan(&e.ID, &e.Query, &e.SourceFilter, &e.ResultsCount, &e.Answer, &e.DurationMS, &createdAt); err != nil {
		return history.SearchEntry{}, err
	}
	t, err := decodeTime(createdAt)
	if err != nil {
		return history.SearchEntry{}, fmt.Errorf("decode created_at: %w", err)
	}
	if t != nil {
		e.CreatedAt = *t
	}
	return e, nil
}

func scanAskRun(row scanner) (history.AskRunEntry, error) {
	var e history.AskRunEntry
	var createdAt string
	var finishedAt any
	if err := row.Scan(&e.ID, &e.Query, &e.Status, &e.GraphJSON, &createdAt, &finishedAt); err != nil {
		return history.AskRunEntry{}, err
	}
	t, err := decodeTime(createdAt)
	if err != nil {
		return history.AskRunEntry{}, fmt.Errorf("decode created_at: %w", err)
	}
	if t != nil {
		e.CreatedAt = *t
	}
	if e.FinishedAt, err = decodeTime(finishedAt); err != nil {
		return history.AskRunEntry{}, fmt.Errorf("decode finished_at: %w", err)
	}
	return e, nil
}
