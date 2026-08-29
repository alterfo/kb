// Package history defines the storage interface for search queries and ask
// runs, so the dashboard can show progress indicators and a persistent
// history without depending on a concrete storage backend.
package history

import (
	"context"
	"time"
)

// Ask run statuses. "Interrupted" marks a run that was still "running" when
// the process last stopped: its goroutine died with the old process, so it
// can never finish, and leaving it "running" forever would be misleading.
const (
	AskRunStatusRunning     = "running"
	AskRunStatusDone        = "done"
	AskRunStatusInterrupted = "interrupted"
)

type SearchEntry struct {
	ID           int64
	Query        string
	SourceFilter string
	ResultsCount int
	Answer       string
	DurationMS   int64
	CreatedAt    time.Time
}

type AskRunEntry struct {
	ID         string
	Query      string
	Status     string
	GraphJSON  string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

type Store interface {
	// RecordSearch logs a completed search. Callers should treat errors as
	// fail-open: history is diagnostic, not load-bearing for the search
	// response itself.
	RecordSearch(ctx context.Context, query, sourceFilter string, resultsCount int, answer string, duration time.Duration, at time.Time) error
	// SearchHistory returns the most recent searches, newest first, capped
	// at limit.
	SearchHistory(ctx context.Context, limit int) ([]SearchEntry, error)
	// SearchEntryByID looks up a single saved search by id. The second
	// return value reports whether the row exists.
	SearchEntryByID(ctx context.Context, id int64) (SearchEntry, bool, error)

	// SaveAskRun upserts a run snapshot; called on create and on every
	// progress/finish update.
	SaveAskRun(ctx context.Context, e AskRunEntry) error
	// AskRuns returns the most recent ask runs, newest first, capped at
	// limit.
	AskRuns(ctx context.Context, limit int) ([]AskRunEntry, error)
	// AskRun looks up a single run by id.
	AskRun(ctx context.Context, id string) (AskRunEntry, bool, error)
	// MarkRunningInterrupted flips every run still "running" to
	// "interrupted"; called once at server startup.
	MarkRunningInterrupted(ctx context.Context) (int, error)
}
