package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestRunReindexCmd_EmbedModelRequiresInto(t *testing.T) {
	env := config.Defaults()
	var stdout, stderr bytes.Buffer
	code := runReindexCmd([]string{"--embed-model=shadow-model"}, env, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--embed-model requires --into") {
		t.Fatalf("stderr = %q, want missing --into error", stderr.String())
	}
}

func TestRunReindexCmd_ShadowReindexWritesAndCompares(t *testing.T) {
	srv := fakeEmbedModelLLMServer(t)
	root := t.TempDir()
	persist := filepath.Join(root, ".persist")
	if err := os.MkdirAll(persist, 0o755); err != nil {
		t.Fatalf("mkdir persist: %v", err)
	}
	writeVerifyDoc(t, root, "notes/shadow-fixture.md", connector.Document{
		ID:     "notes/shadow-fixture",
		Source: "notes",
		Kind:   "note",
		Title:  "Shadow fixture",
		Body:   "The shadow reindex should write an independent candidate index.",
	})

	env := config.Defaults()
	env.KBRoot = root
	env.PersistDir = persist
	env.LLMBaseURL = srv.URL
	env.EmbedBaseURL = srv.URL
	env.EmbedIndexBaseURL = srv.URL
	env.NoProxy = []string{"127.0.0.1"}
	env.IndexGraph = false

	var stdout, stderr bytes.Buffer
	if code := runReindexCmd(nil, env, &stdout, &stderr); code != 0 {
		t.Fatalf("seed reindex code = %d, stderr = %s", code, stderr.String())
	}

	shadowPath := filepath.Join(persist, "shadow.db")
	stdout.Reset()
	stderr.Reset()
	code := runReindexCmd([]string{"--embed-model=shadow-model", "--into=" + shadowPath}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("shadow reindex code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if _, err := os.Stat(shadowPath); err != nil {
		t.Fatalf("shadow db not written: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"shadow reindex: wrote=" + shadowPath,
		"indexed=1",
		"index comparison:",
		"current: corpus_version=",
		"embed_dim=2",
		"shadow: corpus_version=",
		"embed_dim=3",
		"diff: embed_dim current=2 shadow=3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q in:\n%s", want, out)
		}
	}
}

func fakeEmbedModelLLMServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/embeddings":
			var req struct {
				Model string   `json:"model"`
				Input []string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			dim := 2
			if req.Model == "shadow-model" {
				dim = 3
			}
			data := make([]map[string]any, len(req.Input))
			for i := range req.Input {
				vec := make([]float32, dim)
				for j := range vec {
					vec[j] = 0.5
				}
				data[i] = map[string]any{"embedding": vec}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/chat/completions":
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"content": "pong"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
