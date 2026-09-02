package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/store/history"
)

func TestHistoryStoreRecordAndListSearch(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	if err := s.RecordSearch(ctx, "first query", "leon-ai", 3, "answer one", 150*time.Millisecond, now); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	if err := s.RecordSearch(ctx, "second query", "", 0, "answer two", 0, now.Add(time.Second)); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}

	got, err := s.SearchHistory(ctx, 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("SearchHistory: got %d entries, want 2", len(got))
	}
	// Newest first.
	if got[0].Query != "second query" || got[1].Query != "first query" {
		t.Fatalf("SearchHistory: unexpected order: %+v", got)
	}
	if got[1].SourceFilter != "leon-ai" || got[1].ResultsCount != 3 || got[1].DurationMS != 150 {
		t.Fatalf("SearchHistory: unexpected fields: %+v", got[1])
	}
	if got[0].Answer != "answer two" || got[1].Answer != "answer one" {
		t.Fatalf("SearchHistory: unexpected answers: %+v", got)
	}
	if !got[1].CreatedAt.Equal(now) {
		t.Fatalf("SearchHistory: CreatedAt = %v, want %v", got[1].CreatedAt, now)
	}
}

func TestHistoryStoreSearchHistoryEmptyAnswer(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	if err := s.RecordSearch(ctx, "no answer", "wiki", 2, "", 50*time.Millisecond, now); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}

	got, err := s.SearchHistory(ctx, 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("SearchHistory: got %d entries, want 1", len(got))
	}
	if got[0].Answer != "" {
		t.Fatalf("SearchHistory: Answer = %q, want empty", got[0].Answer)
	}
	if got[0].Query != "no answer" || got[0].SourceFilter != "wiki" || got[0].ResultsCount != 2 {
		t.Fatalf("SearchHistory: unexpected fields: %+v", got[0])
	}
}

func TestHistoryStoreSearchHistoryLimit(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		if err := s.RecordSearch(ctx, "q", "", 0, "", 0, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("RecordSearch: %v", err)
		}
	}
	got, err := s.SearchHistory(ctx, 2)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("SearchHistory: got %d entries, want 2", len(got))
	}
}

func TestHistoryStoreSearchHistoryEmpty(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	got, err := s.SearchHistory(context.Background(), 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("SearchHistory: got %d entries, want 0", len(got))
	}
}

func TestHistoryStoreSearchEntryByID(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	if err := s.RecordSearch(ctx, "by id", "notes", 4, "stored answer", 75*time.Millisecond, now); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	listed, err := s.SearchHistory(ctx, 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("SearchHistory: got %d entries, want 1", len(listed))
	}

	got, ok, err := s.SearchEntryByID(ctx, listed[0].ID)
	if err != nil {
		t.Fatalf("SearchEntryByID: %v", err)
	}
	if !ok {
		t.Fatalf("SearchEntryByID: got ok=false, want true")
	}
	if got.Query != "by id" || got.Answer != "stored answer" || got.ResultsCount != 4 || got.DurationMS != 75 {
		t.Fatalf("SearchEntryByID: unexpected fields: %+v", got)
	}
	if !got.CreatedAt.Equal(now) {
		t.Fatalf("SearchEntryByID: CreatedAt = %v, want %v", got.CreatedAt, now)
	}

	_, ok, err = s.SearchEntryByID(ctx, 999999)
	if err != nil {
		t.Fatalf("SearchEntryByID (missing): %v", err)
	}
	if ok {
		t.Fatalf("SearchEntryByID (missing): got ok=true, want false")
	}
}

func TestHistoryStoreSaveAskRunCreateAndUpdate(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	created := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	entry := history.AskRunEntry{
		ID:        "run-1",
		Query:     "what is the roadmap",
		Status:    history.AskRunStatusRunning,
		GraphJSON: `{"query":"what is the roadmap","nodes":[]}`,
		CreatedAt: created,
	}
	if err := s.SaveAskRun(ctx, entry); err != nil {
		t.Fatalf("SaveAskRun (create): %v", err)
	}

	got, ok, err := s.AskRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("AskRun: %v", err)
	}
	if !ok {
		t.Fatalf("AskRun: run-1 not found")
	}
	if got.Status != history.AskRunStatusRunning || got.FinishedAt != nil {
		t.Fatalf("AskRun: unexpected initial state: %+v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("AskRun: CreatedAt = %v, want %v", got.CreatedAt, created)
	}

	finished := created.Add(5 * time.Second)
	entry.Status = history.AskRunStatusDone
	entry.GraphJSON = `{"query":"what is the roadmap","final_answer":"soon"}`
	entry.FinishedAt = &finished
	if err := s.SaveAskRun(ctx, entry); err != nil {
		t.Fatalf("SaveAskRun (update): %v", err)
	}

	got, ok, err = s.AskRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("AskRun: %v", err)
	}
	if !ok {
		t.Fatalf("AskRun: run-1 not found after update")
	}
	if got.Status != history.AskRunStatusDone {
		t.Fatalf("AskRun: Status = %q, want done", got.Status)
	}
	if got.GraphJSON != entry.GraphJSON {
		t.Fatalf("AskRun: GraphJSON not updated: %q", got.GraphJSON)
	}
	if got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatalf("AskRun: FinishedAt = %v, want %v", got.FinishedAt, finished)
	}
	// created_at must not change on update.
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("AskRun: CreatedAt changed on update: %v, want %v", got.CreatedAt, created)
	}
}

func TestHistoryStoreSaveAskRunRequiresID(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	err := s.SaveAskRun(context.Background(), history.AskRunEntry{Query: "no id"})
	if err == nil {
		t.Fatalf("SaveAskRun: expected error for missing id")
	}
}

func TestHistoryStoreAskRunNotFound(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	_, ok, err := s.AskRun(context.Background(), "missing")
	if err != nil {
		t.Fatalf("AskRun: %v", err)
	}
	if ok {
		t.Fatalf("AskRun: expected not found")
	}
}

func TestHistoryStoreAskRunsOrderAndLimit(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	base := time.Now().UTC()

	for i := 0; i < 3; i++ {
		e := history.AskRunEntry{
			ID:        "run-" + string(rune('a'+i)),
			Query:     "q",
			Status:    history.AskRunStatusDone,
			GraphJSON: "{}",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := s.SaveAskRun(ctx, e); err != nil {
			t.Fatalf("SaveAskRun: %v", err)
		}
	}
	got, err := s.AskRuns(ctx, 2)
	if err != nil {
		t.Fatalf("AskRuns: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("AskRuns: got %d, want 2", len(got))
	}
	if got[0].ID != "run-c" || got[1].ID != "run-b" {
		t.Fatalf("AskRuns: unexpected order: %+v", got)
	}
}

func TestHistoryStoreMarkRunningInterrupted(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	running := history.AskRunEntry{ID: "r1", Query: "q1", Status: history.AskRunStatusRunning, GraphJSON: "{}", CreatedAt: now}
	done := history.AskRunEntry{ID: "r2", Query: "q2", Status: history.AskRunStatusDone, GraphJSON: "{}", CreatedAt: now}
	if err := s.SaveAskRun(ctx, running); err != nil {
		t.Fatalf("SaveAskRun: %v", err)
	}
	if err := s.SaveAskRun(ctx, done); err != nil {
		t.Fatalf("SaveAskRun: %v", err)
	}

	n, err := s.MarkRunningInterrupted(ctx)
	if err != nil {
		t.Fatalf("MarkRunningInterrupted: %v", err)
	}
	if n != 1 {
		t.Fatalf("MarkRunningInterrupted: affected %d rows, want 1", n)
	}

	r1, _, err := s.AskRun(ctx, "r1")
	if err != nil {
		t.Fatalf("AskRun r1: %v", err)
	}
	if r1.Status != history.AskRunStatusInterrupted {
		t.Fatalf("r1.Status = %q, want interrupted", r1.Status)
	}
	r2, _, err := s.AskRun(ctx, "r2")
	if err != nil {
		t.Fatalf("AskRun r2: %v", err)
	}
	if r2.Status != history.AskRunStatusDone {
		t.Fatalf("r2.Status = %q, want done (unaffected)", r2.Status)
	}

	// A second call with nothing running affects 0 rows.
	n, err = s.MarkRunningInterrupted(ctx)
	if err != nil {
		t.Fatalf("MarkRunningInterrupted (2nd): %v", err)
	}
	if n != 0 {
		t.Fatalf("MarkRunningInterrupted (2nd): affected %d rows, want 0", n)
	}
}

func TestHistoryStoreSearchHistoryOrdersByCreatedAt(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	if err := s.RecordSearch(ctx, "inserted first but newest", "", 1, "", 0, now.Add(time.Minute)); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	if err := s.RecordSearch(ctx, "inserted second but oldest", "", 2, "", 0, now); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}

	got, err := s.SearchHistory(ctx, 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("SearchHistory: got %d entries, want 2", len(got))
	}
	if got[0].Query != "inserted first but newest" || got[1].Query != "inserted second but oldest" {
		t.Fatalf("SearchHistory: ordered by id, want by created_at: %+v", got)
	}
}

func TestHistoryStoreRecordSearchDocumentIDs(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	if err := s.RecordSearch(ctx, "q", "", 2, "", 0, now, "doc-a", "doc-b"); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}

	got, err := s.SearchHistory(ctx, 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("SearchHistory: got %d entries, want 1", len(got))
	}
	if len(got[0].DocumentIDs) != 2 || got[0].DocumentIDs[0] != "doc-a" || got[0].DocumentIDs[1] != "doc-b" {
		t.Fatalf("DocumentIDs = %v, want [doc-a doc-b]", got[0].DocumentIDs)
	}
	if got[0].Feedback != history.FeedbackNone {
		t.Fatalf("Feedback = %d, want none", got[0].Feedback)
	}
}

func TestHistoryStoreRecordFeedbackAndFeedbackByDoc(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	if err := s.RecordSearch(ctx, "up", "", 1, "", 0, now, "doc-a", "doc-b"); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	if err := s.RecordSearch(ctx, "down", "", 1, "", 0, now, "doc-b", "doc-c"); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	entries, err := s.SearchHistory(ctx, 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("SearchHistory: got %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		switch e.Query {
		case "up":
			if err := s.RecordFeedback(ctx, e.ID, history.FeedbackUp, now); err != nil {
				t.Fatalf("RecordFeedback up: %v", err)
			}
		case "down":
			if err := s.RecordFeedback(ctx, e.ID, history.FeedbackDown, now); err != nil {
				t.Fatalf("RecordFeedback down: %v", err)
			}
		}
	}

	prior, err := s.FeedbackByDoc(ctx)
	if err != nil {
		t.Fatalf("FeedbackByDoc: %v", err)
	}
	if prior["doc-a"] != 1 {
		t.Errorf("prior[doc-a] = %v, want 1", prior["doc-a"])
	}
	if prior["doc-b"] != 0 {
		t.Errorf("prior[doc-b] = %v, want 0 (up+down cancels)", prior["doc-b"])
	}
	if prior["doc-c"] != -1 {
		t.Errorf("prior[doc-c] = %v, want -1", prior["doc-c"])
	}
}

func TestHistoryStoreRecordFeedbackInvalidValue(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	if err := s.RecordSearch(ctx, "q", "", 0, "", 0, now); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	entries, err := s.SearchHistory(ctx, 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if err := s.RecordFeedback(ctx, entries[0].ID, 2, now); err == nil {
		t.Fatal("RecordFeedback with feedback=2 should fail")
	}
}

func TestHistoryStoreLabeledEval(t *testing.T) {
	db := openTestDB(t)
	s := NewHistoryStore(db)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	if err := s.RecordSearch(ctx, "relevant query", "", 1, "", 0, now, "doc-a"); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	if err := s.RecordSearch(ctx, "irrelevant query", "", 1, "", 0, now, "doc-b"); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	if err := s.RecordSearch(ctx, "unrated query", "", 1, "", 0, now, "doc-c"); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	entries, err := s.SearchHistory(ctx, 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	for _, e := range entries {
		switch e.Query {
		case "relevant query":
			if err := s.RecordFeedback(ctx, e.ID, history.FeedbackUp, now); err != nil {
				t.Fatalf("RecordFeedback up: %v", err)
			}
		case "irrelevant query":
			if err := s.RecordFeedback(ctx, e.ID, history.FeedbackDown, now); err != nil {
				t.Fatalf("RecordFeedback down: %v", err)
			}
		}
	}

	examples, err := s.LabeledEval(ctx)
	if err != nil {
		t.Fatalf("LabeledEval: %v", err)
	}
	if len(examples) != 2 {
		t.Fatalf("LabeledEval: got %d examples, want 2", len(examples))
	}
	labels := map[string]string{examples[0].Query: examples[0].Label, examples[1].Query: examples[1].Label}
	if labels["relevant query"] != history.LabelRelevant {
		t.Errorf("relevant query label = %q", labels["relevant query"])
	}
	if labels["irrelevant query"] != history.LabelNotRelevant {
		t.Errorf("irrelevant query label = %q", labels["irrelevant query"])
	}
	for _, e := range examples {
		if e.Provenance != "user-feedback" {
			t.Errorf("provenance = %q, want user-feedback", e.Provenance)
		}
		if e.LabeledAt.IsZero() {
			t.Errorf("LabeledAt should be set for %q", e.Query)
		}
	}
}
