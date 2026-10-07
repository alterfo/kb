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

// Ask message roles for follow-up threads attached to an ask run.
const (
	AskRoleUser      = "user"
	AskRoleAssistant = "assistant"
)

// Search feedback values. FeedbackNone means the user has not rated the
// search; FeedbackUp/FeedbackDown are the two explicit ratings.
const (
	FeedbackNone = 0
	FeedbackUp   = 1
	FeedbackDown = -1
)

// Feedback labels exported into the labeled eval set.
const (
	LabelRelevant    = "relevant"
	LabelNotRelevant = "not_relevant"
)

type SearchEntry struct {
	ID           int64
	Query        string
	SourceFilter string
	ResultsCount int
	Answer       string
	DurationMS   int64
	CreatedAt    time.Time
	DocumentIDs  []string
	Feedback     int
}

// LabeledExample is one feedback-derived eval record: a query, the documents
// that were retrieved for it, and a relevance label. Provenance records
// where the label came from (currently always user feedback on the search
// dashboard) so downstream evals can trace labels back to their source.
type LabeledExample struct {
	Query       string    `json:"query"`
	DocumentIDs []string  `json:"document_ids"`
	Label       string    `json:"label"`
	Provenance  string    `json:"provenance"`
	LabeledAt   time.Time `json:"labeled_at"`
}

type AskRunEntry struct {
	ID         string
	Query      string
	Status     string
	GraphJSON  string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

// AskMessage is one message in a follow-up thread attached to an ask run.
// Sources holds the source identifiers cited by an assistant message;
// WebUsed records whether untrusted web results fed that message.
type AskMessage struct {
	RunID     string
	Seq       int
	Role      string
	Content   string
	Sources   []string
	WebUsed   bool
	CreatedAt time.Time
}

type Store interface {
	// RecordSearch logs a completed search. Callers should treat errors as
	// fail-open: history is diagnostic, not load-bearing for the search
	// response itself. documentIDs optionally records which documents were
	// retrieved, so a later rating can be turned into a per-document prior.
	RecordSearch(ctx context.Context, query, sourceFilter string, resultsCount int, answer string, duration time.Duration, at time.Time, documentIDs ...string) error
	// SearchHistory returns the most recent searches, newest first, capped
	// at limit.
	SearchHistory(ctx context.Context, limit int) ([]SearchEntry, error)
	// SearchEntryByID looks up a single saved search by id. The second
	// return value reports whether the row exists.
	SearchEntryByID(ctx context.Context, id int64) (SearchEntry, bool, error)
	// RecordFeedback attaches a thumbs-up/thumbs-down rating to a saved
	// search. feedback must be one of FeedbackNone/Up/Down.
	RecordFeedback(ctx context.Context, id int64, feedback int, at time.Time) error
	// FeedbackByDoc aggregates user feedback into a per-document score
	// suitable as a retrieval prior: the sum of ratings across every search
	// that retrieved that document.
	FeedbackByDoc(ctx context.Context) (map[string]float64, error)
	// LabeledEval exports the feedback-derived labeled eval set with
	// labeling provenance.
	LabeledEval(ctx context.Context) ([]LabeledExample, error)

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

	// AppendAskMessage appends a message to the follow-up thread of an ask
	// run. The message sequence is assigned automatically per run, so the
	// caller appends in conversational order without managing Seq.
	AppendAskMessage(ctx context.Context, m AskMessage) error
	// AskThread returns the follow-up messages for a run, ordered by seq.
	AskThread(ctx context.Context, runID string) ([]AskMessage, error)
}
