package dragon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveSubmission_WritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "answers.json")
	entries := map[string]SubmissionEntry{
		"0": {FoundIDs: []string{"3", "7"}, ModelAnswer: "ответ ноль"},
		"1": {FoundIDs: nil, ModelAnswer: "не хватает данных в контексте"},
	}

	if err := SaveSubmission(path, entries); err != nil {
		t.Fatalf("SaveSubmission: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got map[string]SubmissionEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got["0"].ModelAnswer != "ответ ноль" || len(got["0"].FoundIDs) != 2 {
		t.Errorf("got[0] = %+v", got["0"])
	}
	if got["1"].ModelAnswer != "не хватает данных в контексте" {
		t.Errorf("got[1] = %+v", got["1"])
	}
}

func TestSaveSubmission_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "answers.json")

	if err := SaveSubmission(path, map[string]SubmissionEntry{"0": {ModelAnswer: "ok"}}); err != nil {
		t.Fatalf("SaveSubmission: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat: %v", err)
	}
}

func TestLoadSubmission_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "answers.json")
	want := map[string]SubmissionEntry{
		"0": {FoundIDs: []string{"3", "7"}, ModelAnswer: "ответ ноль"},
		"1": {FoundIDs: nil, ModelAnswer: "answer one"},
	}
	if err := SaveSubmission(path, want); err != nil {
		t.Fatalf("SaveSubmission: %v", err)
	}

	got, err := LoadSubmission(path)
	if err != nil {
		t.Fatalf("LoadSubmission: %v", err)
	}
	if len(got) != 2 || got["0"].ModelAnswer != "ответ ноль" || got["1"].ModelAnswer != "answer one" {
		t.Fatalf("got = %+v", got)
	}
}

func TestLoadSubmission_MissingFile(t *testing.T) {
	if _, err := LoadSubmission("/nonexistent/path/answers.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadSubmission_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadSubmission(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSaveSubmission_EmptyMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "answers.json")
	if err := SaveSubmission(path, map[string]SubmissionEntry{}); err != nil {
		t.Fatalf("SaveSubmission: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got map[string]SubmissionEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want empty", got)
	}
}
