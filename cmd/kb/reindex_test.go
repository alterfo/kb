package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
)

// TestRunReindexCmd_IndexesFixtureTree closes the gap left by
// TestRun_ReindexOnEmptyRootIsNoop (main_test.go), which only exercises the
// empty-root no-op path. This drives runReindexCmd against a real fixture
// document and a fake LLM backend (the same fakeLLMServer used by
// doctor_test.go) end to end, asserting the command actually indexes it.
func TestRunReindexCmd_IndexesFixtureTree(t *testing.T) {
	srv := fakeLLMServer(t)
	root := t.TempDir()
	persist := filepath.Join(root, ".persist")
	if err := os.MkdirAll(persist, 0o755); err != nil {
		t.Fatalf("mkdir persist: %v", err)
	}
	writeVerifyDoc(t, root, "notes/reindex-fixture.md", connector.Document{
		ID:     "notes/reindex-fixture",
		Source: "notes",
		Kind:   "note",
		Title:  "Reindex fixture",
		Body:   "The reindex command should pick up this document.",
	})

	env := config.Defaults()
	env.KBRoot = root
	env.PersistDir = persist
	env.LLMBaseURL = srv.URL
	env.NoProxy = []string{"127.0.0.1"}

	var stdout, stderr bytes.Buffer
	code := runReindexCmd(nil, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runReindexCmd code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "indexed=1") {
		t.Fatalf("stdout = %q, want indexed=1", stdout.String())
	}

	// A second run over the unchanged tree should skip, not re-index.
	stdout.Reset()
	code = runReindexCmd(nil, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second runReindexCmd code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "indexed=0") || !strings.Contains(stdout.String(), "skipped=1") {
		t.Fatalf("second run stdout = %q, want indexed=0 skipped=1", stdout.String())
	}
}

func TestRunReindexCmd_FlagParseErrorExitsTwo(t *testing.T) {
	env := config.Defaults()
	var stdout, stderr bytes.Buffer
	code := runReindexCmd([]string{"--not-a-flag"}, env, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
