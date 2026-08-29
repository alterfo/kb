package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runbench "github.com/alterfo/kb/internal/bench/run"
	"github.com/alterfo/kb/internal/config"
)

func writeReportFile(t *testing.T, dir, name string, rep *runbench.Report) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := runbench.SaveReport(path, rep); err != nil {
		t.Fatalf("SaveReport: %v", err)
	}
	return path
}

func TestBenchCompareHappyPath(t *testing.T) {
	dir := t.TempDir()
	baseline := writeReportFile(t, dir, "baseline.json", &runbench.Report{
		Total:        20,
		AbstainTotal: 2,
		Types:        map[string]*runbench.TypeStat{"basic": {Count: 10, AvgRecall: 0.6, Abstain: 1, Cited: 5}},
		Languages:    map[string]*runbench.TypeStat{"ru": {Count: 10, AvgRecall: 0.6, Abstain: 1, Cited: 5}},
	})
	candidate := writeReportFile(t, dir, "candidate.json", &runbench.Report{
		Total:        20,
		AbstainTotal: 3,
		Types:        map[string]*runbench.TypeStat{"basic": {Count: 10, AvgRecall: 0.8, Abstain: 0, Cited: 7}},
		Languages:    map[string]*runbench.TypeStat{"ru": {Count: 10, AvgRecall: 0.8, Abstain: 0, Cited: 7}},
	})

	var stdout, stderr bytes.Buffer
	code := runBenchCmd([]string{"compare", baseline, candidate}, config.Env{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{
		"total=+0 abstain=+1",
		"languages:",
		"ru",
		"recall=+0.200 abstain=-1 cited=+2",
		"types:",
		"basic",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
		}
	}
}

func TestBenchCompareWritesOutJSON(t *testing.T) {
	dir := t.TempDir()
	baseline := writeReportFile(t, dir, "baseline.json", &runbench.Report{Total: 1})
	candidate := writeReportFile(t, dir, "candidate.json", &runbench.Report{Total: 2})
	out := filepath.Join(dir, "compare.json")

	var stdout, stderr bytes.Buffer
	code := runBenchCmd([]string{"compare", "-out", out, baseline, candidate}, config.Env{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read compare output: %v", err)
	}
	var res runbench.CompareResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("unmarshal compare output: %v", err)
	}
	if res.TotalDelta != 1 {
		t.Errorf("TotalDelta = %d, want 1", res.TotalDelta)
	}
}

func TestBenchCompareMissingArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBenchCmd([]string{"compare"}, config.Env{}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "expected two report files") {
		t.Errorf("stderr = %q, want usage error", stderr.String())
	}
}

func TestBenchCompareUnreadableFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runBenchCmd([]string{
		"compare",
		filepath.Join(t.TempDir(), "missing-baseline.json"),
		filepath.Join(t.TempDir(), "missing-candidate.json"),
	}, config.Env{}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "read report") {
		t.Errorf("stderr = %q, want read error", stderr.String())
	}
}

func TestBenchCompareInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidate := writeReportFile(t, dir, "candidate.json", &runbench.Report{Total: 1})

	var stdout, stderr bytes.Buffer
	code := runBenchCmd([]string{"compare", bad, candidate}, config.Env{}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "parse report") {
		t.Errorf("stderr = %q, want parse error", stderr.String())
	}
}
