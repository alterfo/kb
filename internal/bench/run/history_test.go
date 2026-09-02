package run

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReportHistoryAppendAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bench-history.json")
	first := &Report{Total: 3, Types: map[string]*TypeStat{"basic": {Count: 3}}}
	second := &Report{Total: 4, AbstainTotal: 1, Languages: map[string]*TypeStat{"ru": {Count: 4}}}

	if err := appendReportHistoryAt(path, first, time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := appendReportHistoryAt(path, second, time.Date(2026, 9, 2, 10, 1, 0, 0, time.UTC)); err != nil {
		t.Fatalf("second append: %v", err)
	}

	entries, err := LoadReportHistory(path)
	if err != nil {
		t.Fatalf("LoadReportHistory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("history length = %d, want 2", len(entries))
	}
	if entries[0].Report.Total != 3 || entries[0].Timestamp.Hour() != 10 {
		t.Fatalf("first entry = %+v", entries[0])
	}
	if entries[1].Report.Total != 4 || entries[1].Timestamp.Minute() != 1 {
		t.Fatalf("second entry = %+v", entries[1])
	}
}

func TestReportHistoryLoadMissingFile(t *testing.T) {
	if _, err := LoadReportHistory(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("LoadReportHistory on missing file returned nil error")
	}
}
