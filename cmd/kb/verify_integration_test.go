//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/verify/qa"
)

// minHitRate reads KB_VERIFY_MIN_HITRATE, falling back to a conservative
// default so this doesn't flap on model-to-model variance while still
// catching a hit-rate regression (previously this test only asserted the
// command exited 0 and printed *some* rate, never the value itself).
func minHitRate() float64 {
	if v := os.Getenv("KB_VERIFY_MIN_HITRATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0.5
}

func TestRunVerifyCmd_LiveQA(t *testing.T) {
	if os.Getenv("KB_LLM_IT") != "1" {
		t.Skip("KB_LLM_IT != 1, skipping live-LLM QA integration test")
	}

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

	env, err := config.LoadEnv(os.LookupEnv)
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	env.KBRoot = root
	env.PersistDir = persist

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
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	var rep qa.Report
	if err := json.Unmarshal(reportData, &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if want := minHitRate(); rep.Rate() < want {
		t.Errorf("QA hit-rate = %.3f (%d/%d), want >= %.3f (override with KB_VERIFY_MIN_HITRATE)",
			rep.Rate(), rep.Passed, rep.Asked, want)
	}
}
