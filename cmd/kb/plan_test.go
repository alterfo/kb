package main

import (
	"os"
	"testing"

	"github.com/alterfo/kb/internal/config"
)

func devNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening devnull: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestPlanConfigFromEnvOverrides(t *testing.T) {
	t.Setenv("KB_PLAN_BASE_URL", "http://llm.example")
	t.Setenv("KB_PLAN_API_KEY", "secret-key")
	t.Setenv("KB_PLAN_MODEL", "model-x")
	t.Setenv("KB_PLAN_DIR", "plans-dir")
	t.Setenv("KB_PLAN_PROGRESS_DIR", "progress-dir")

	cfg := planConfigFromEnv(config.Defaults())
	if cfg.baseURL != "http://llm.example" {
		t.Errorf("baseURL = %q, want override", cfg.baseURL)
	}
	if cfg.apiKey != "secret-key" {
		t.Errorf("apiKey = %q, want override", cfg.apiKey)
	}
	if cfg.model != "model-x" {
		t.Errorf("model = %q, want override", cfg.model)
	}
	if cfg.plansDir != "plans-dir" {
		t.Errorf("plansDir = %q, want override", cfg.plansDir)
	}
	if cfg.progressDir != "progress-dir" {
		t.Errorf("progressDir = %q, want override", cfg.progressDir)
	}
}

func TestRunPlanCmd_ArgParsingErrorsExitTwo(t *testing.T) {
	devNullOut := devNull(t)
	env := config.Defaults()
	cases := []struct {
		name string
		args []string
	}{
		{"new without description", []string{"--new"}},
		{"max-iterations without value", []string{"--max-iterations"}},
		{"invalid max-iterations", []string{"--max-iterations", "0"}},
		{"non-numeric max-iterations", []string{"--max-iterations", "abc"}},
		{"missing plan file", []string{"--no-commit"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := runPlanCmd(tc.args, env, devNullOut, devNullOut); code != 2 {
				t.Errorf("runPlanCmd(%v) = %d, want 2", tc.args, code)
			}
		})
	}
}
