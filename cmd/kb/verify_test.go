package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/verify/qa"
)

func writeVerifyDoc(t *testing.T, root, rel string, doc connector.Document) {
	t.Helper()
	data, err := render.Render(doc)
	if err != nil {
		t.Fatalf("render.Render: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel))), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestRunVerifyCmd_BuildGolden(t *testing.T) {
	root := t.TempDir()
	writeVerifyDoc(t, root, "leon-ai/closed.md", connector.Document{
		ID: "leon-ai/leon#1", Source: "leon-ai", Kind: "issue", Title: "the question", Body: "the expected answer",
		Frontmatter: map[string]any{"state": "closed"},
	})
	writeVerifyDoc(t, root, "leon-ai/open.md", connector.Document{
		ID: "leon-ai/leon#2", Source: "leon-ai", Kind: "issue", Title: "skip me", Body: "body",
		Frontmatter: map[string]any{"state": "open"},
	})

	out := filepath.Join(t.TempDir(), "qa_pairs.json")
	env := config.Defaults()
	env.KBRoot = root
	var stdout, stderr bytes.Buffer
	code := runVerifyCmd([]string{"--build-golden", "--golden-out", out, "--source", "leon-ai"}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runVerifyCmd code = %d, stderr = %s", code, stderr.String())
	}
	pairs, err := qaPairsFromFile(t, out)
	if err != nil {
		t.Fatalf("load generated pairs: %v", err)
	}
	if len(pairs) != 1 || pairs[0].Question != "the question" {
		t.Fatalf("pairs = %+v", pairs)
	}
}

// TestRunVerifyCmd_OfflineQAHappyPath is the offline counterpart to
// TestRunVerifyCmd_LiveQA (verify_integration_test.go), which is gated
// behind KB_LLM_IT=1 and never runs in CI. It drives the same
// index -> verify -> report flow against the fake LLM backend used by
// doctor_test.go instead of a live model, so the QA hit-rate path has at
// least one CI-covered exercise.
func TestRunVerifyCmd_OfflineQAHappyPath(t *testing.T) {
	srv := fakeLLMServer(t)
	root := t.TempDir()
	persist := filepath.Join(root, ".persist")
	if err := os.MkdirAll(persist, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeVerifyDoc(t, root, "leon-ai/answer.md", connector.Document{
		ID:      "leon-ai/leon#9000",
		Source:  "leon-ai",
		Kind:    "note",
		Title:   "Answer",
		Body:    "The dependency error is fixed by running npm install from the project root.",
		Summary: "Fix dependency errors with npm install.",
	})

	pairsPath := filepath.Join(root, "qa_pairs.json")
	if err := qa.WriteQAPairs(pairsPath, []qa.QAPair{{
		ID:       "leon-ai/leon#9000",
		Question: "How do I fix a missing dependency error?",
		Expected: "Run npm install from the project root.",
	}}); err != nil {
		t.Fatalf("WriteQAPairs: %v", err)
	}

	env := config.Defaults()
	env.KBRoot = root
	env.PersistDir = persist
	env.LLMBaseURL = srv.URL
	env.NoProxy = []string{"127.0.0.1"}

	ctx := context.Background()
	bundle, err := newEngineBundle(env)
	if err != nil {
		t.Fatalf("newEngineBundle: %v", err)
	}
	defer bundle.close()
	if err := bundle.indexer.AddOrUpdateDocument(ctx, "leon-ai/answer.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	reportPath := filepath.Join(persist, "last-qa-report.json")
	var stdout, stderr bytes.Buffer
	code := runVerifyCmd([]string{"--pairs", pairsPath, "--report", reportPath, "--top-k", "3"}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runVerifyCmd code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "QA hit-rate:") {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("report not written: %v", err)
	}
}

func TestRunVerifyCmd_EmptyPairsDoesNotOpenDB(t *testing.T) {
	pairsPath := filepath.Join(t.TempDir(), "qa_pairs.json")
	if err := os.WriteFile(pairsPath, []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	env := config.Defaults()
	var stdout, stderr bytes.Buffer
	code := runVerifyCmd([]string{"--pairs", pairsPath}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runVerifyCmd code = %d, stderr = %s", code, stderr.String())
	}
}

func qaPairsFromFile(t *testing.T, path string) ([]struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Expected string `json:"expected"`
}, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var pairs []struct {
		ID       string `json:"id"`
		Question string `json:"question"`
		Expected string `json:"expected"`
	}
	dec := json.NewDecoder(f)
	if err := dec.Decode(&pairs); err != nil {
		return nil, err
	}
	return pairs, nil
}
