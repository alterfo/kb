package dragon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type SubmissionEntry struct {
	FoundIDs    []string `json:"found_ids"`
	ModelAnswer string   `json:"model_answer"`
}

func LoadSubmission(path string) (map[string]SubmissionEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dragon: read submission: %w", err)
	}
	var entries map[string]SubmissionEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("dragon: parse submission: %w", err)
	}
	return entries, nil
}

func SaveSubmission(path string, entries map[string]SubmissionEntry) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("dragon: create submission dir: %w", err)
		}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("dragon: encode submission: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("dragon: write submission: %w", err)
	}
	return nil
}
