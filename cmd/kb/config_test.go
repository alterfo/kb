package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/config"
)

func mapLookup(m map[string]string) config.EnvLookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestRunConfigShow(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runConfigCmd([]string{"show"}, config.Defaults(), mapLookup(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runConfigCmd(show) = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	for _, want := range []string{"KB_TOP_K=10", "KB_RERANK=off", "KB_SOCKS_PROXY=(unset)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("config show output missing %q\n%s", want, stdout.String())
		}
	}
}

func TestRunConfigShow_PresetQuality(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runConfigCmd([]string{"show", "--preset", "quality"}, config.Defaults(), mapLookup(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runConfigCmd(show --preset quality) = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	for _, want := range []string{"KB_RERANK=llm", "KB_QUALIFIER_FILTER=true", "KB_MAX_SUBGOALS=8", "KB_MAX_GAP_QUERIES=5", "KB_CANDIDATE_K=40"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("quality preset output missing %q\n%s", want, stdout.String())
		}
	}
}

func TestRunConfigShow_BadPreset(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runConfigCmd([]string{"show", "--preset", "turbo"}, config.Defaults(), mapLookup(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("runConfigCmd(show --preset turbo) = %d, want 2", code)
	}
}

func TestRunConfigShow_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runConfigCmd([]string{"hide"}, config.Defaults(), mapLookup(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("runConfigCmd(hide) = %d, want 2", code)
	}
}
