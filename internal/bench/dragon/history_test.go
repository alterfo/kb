package dragon

import (
	"path/filepath"
	"testing"
	"time"
)

func TestScoreHistoryAppendAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dragon-score-history.json")
	first := &ScoreReport{Total: 20, Matched: 10, RetrievalHits: 8}
	second := &ScoreReport{Total: 20, Matched: 11, RetrievalHits: 9}

	if err := appendScoreHistoryAt(path, first, time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := appendScoreHistoryAt(path, second, time.Date(2026, 9, 2, 12, 1, 0, 0, time.UTC)); err != nil {
		t.Fatalf("second append: %v", err)
	}

	entries, err := LoadScoreHistory(path)
	if err != nil {
		t.Fatalf("LoadScoreHistory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("history length = %d, want 2", len(entries))
	}
	if entries[0].Report.Matched != 10 || entries[1].Report.Matched != 11 {
		t.Fatalf("entries = %+v", entries)
	}
}
