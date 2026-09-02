package dragon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ScoreHistoryEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Report    ScoreReport `json:"report"`
}

func AppendScoreHistory(path string, rep *ScoreReport) error {
	return appendScoreHistoryAt(path, rep, time.Now().UTC())
}

func appendScoreHistoryAt(path string, rep *ScoreReport, timestamp time.Time) error {
	entries, err := LoadScoreHistory(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	entries = append(entries, ScoreHistoryEntry{Timestamp: timestamp, Report: *rep})
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("dragon: encode score history: %w", err)
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("dragon: create score history dir: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("dragon: write score history: %w", err)
	}
	return nil
}

func LoadScoreHistory(path string) ([]ScoreHistoryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []ScoreHistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("dragon: parse score history: %w", err)
	}
	return entries, nil
}
