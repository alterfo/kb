package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ReportHistoryEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Report    Report    `json:"report"`
}

func AppendReportHistory(path string, rep *Report) error {
	return appendReportHistoryAt(path, rep, time.Now().UTC())
}

func appendReportHistoryAt(path string, rep *Report, timestamp time.Time) error {
	entries, err := LoadReportHistory(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	entries = append(entries, ReportHistoryEntry{Timestamp: timestamp, Report: *rep})
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: encode metrics history: %w", err)
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("bench: create metrics history dir: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("bench: write metrics history: %w", err)
	}
	return nil
}

func LoadReportHistory(path string) ([]ReportHistoryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []ReportHistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("bench: parse metrics history: %w", err)
	}
	return entries, nil
}
