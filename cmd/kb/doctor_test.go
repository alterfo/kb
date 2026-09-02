package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/state"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

func fakeLLMServer(t *testing.T) *httptest.Server {
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
			data := make([]map[string]any, len(req.Input))
			for i := range req.Input {
				data[i] = map[string]any{"embedding": []float32{0.6, 0.8}}
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

func seedDoctorEnv(t *testing.T, srvURL string) config.Env {
	t.Helper()
	root := t.TempDir()
	persist := filepath.Join(root, ".persist")
	if err := os.MkdirAll(persist, 0o755); err != nil {
		t.Fatalf("mkdir persist: %v", err)
	}

	sources := `sources:
  - name: my-gh
    type: github
    config:
      repo: kb
    secrets:
      token: GITHUB_TOKEN
  - name: fresh-wiki
    type: wiki
    secrets:
      token: WIKI_TOKEN
  - name: stale-wiki
    type: wiki
    secrets:
      token: WIKI_TOKEN
  - name: never-gitlab
    type: gitlab
    secrets:
      token: GITLAB_TOKEN
`
	if err := os.WriteFile(filepath.Join(root, "sources.yaml"), []byte(sources), 0o644); err != nil {
		t.Fatalf("write sources.yaml: %v", err)
	}

	st, err := state.OpenStore(filepath.Join(persist, ".sync-state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	now := time.Now()
	if err := st.Advance("github:my-gh", "c1", now.Add(-30*time.Hour)); err != nil {
		t.Fatalf("Advance stale: %v", err)
	}
	if err := st.Advance("wiki:fresh-wiki", "c2", now.Add(-time.Hour)); err != nil {
		t.Fatalf("Advance fresh: %v", err)
	}
	if err := st.RecordError("wiki:stale-wiki", now.Add(-60*time.Hour), "sync boom"); err != nil {
		t.Fatalf("RecordError: %v", err)
	}

	db, err := sqlite.Open(context.Background(), filepath.Join(persist, "kb.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	vs := sqlite.NewVectorStore(db)
	if err := vs.EnsureDim(context.Background(), 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}
	if err := vs.Upsert(context.Background(), []vector.Chunk{
		{ID: "a", RefDocID: "doc1", Text: "alpha", FilePath: "notes/a.md", FileName: "a.md", Source: "notes", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc2", Text: "beta", FilePath: "notes/b.md", FileName: "b.md", Source: "notes", Embedding: []float32{0, 1}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	gs := sqlite.NewGraphStore(db)
	if err := gs.UpsertEntities(context.Background(), []graphstore.Entity{
		{ID: "e1", Name: "KB", Type: "project", SourceChunks: []string{"a"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := gs.UpsertRelations(context.Background(), []graphstore.Relation{
		{ID: "r1", Src: "e1", Dst: "e1", Type: "relates", Weight: 1, SourceChunks: []string{"a"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	if err := gs.UpsertCommunities(context.Background(), []graphstore.Community{
		{ID: "c1", Level: 0, Members: []string{"e1"}, Summary: "s", Title: "t"},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	env := config.Defaults()
	env.KBRoot = root
	env.PersistDir = persist
	env.LLMBaseURL = srvURL
	env.EmbedBaseURL = srvURL
	env.EmbedIndexBaseURL = srvURL
	env.NoProxy = []string{"127.0.0.1"}
	return env
}

func TestDoctor_HealthyReport(t *testing.T) {
	srv := fakeLLMServer(t)
	t.Setenv("GITHUB_TOKEN", "ghp_secretvalue")
	env := seedDoctorEnv(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runDoctorCmd(nil, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runDoctorCmd = %d, stderr=%s, stdout=%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"llm chat (host):  " + srv.URL,
		"query embed (local): " + srv.URL,
		"index embed (bulk):   " + srv.URL,
		"query embed dim: ok (2)",
		"index embed dim: ok (2)",
		"chat: ok",
		"integrity: ok",
		"status: ok (corpus_version=",
		"embed_dim=2",
		"chunks=2",
		"entities=1",
		"relations=1",
		"communities=1",
		"my-gh (github)",
		"token=set",
		"fresh-wiki (wiki)",
		"stale-wiki (wiki)",
		"never-gitlab (gitlab)",
		"token=unset",
		"last_error=\"sync boom\"",
		"never synced (stale)",
		"doctor: all checks passed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q in:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "  - my-gh"):
			if !strings.Contains(line, "(stale)") {
				t.Errorf("my-gh should be stale (30h > 24h), line: %q", line)
			}
		case strings.HasPrefix(line, "  - fresh-wiki"):
			if !strings.Contains(line, "(fresh)") {
				t.Errorf("fresh-wiki should be fresh, line: %q", line)
			}
		case strings.HasPrefix(line, "  - stale-wiki"):
			if !strings.Contains(line, "(stale)") {
				t.Errorf("stale-wiki should be stale, line: %q", line)
			}
		case strings.HasPrefix(line, "  - never-gitlab"):
			if !strings.Contains(line, "never synced (stale)") {
				t.Errorf("never-gitlab should be never synced, line: %q", line)
			}
		}
	}
}

func TestDoctor_SecretValuesNeverLeak(t *testing.T) {
	srv := fakeLLMServer(t)
	t.Setenv("GITHUB_TOKEN", "ghp_super-secret-value-42")
	env := seedDoctorEnv(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runDoctorCmd(nil, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runDoctorCmd = %d, stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "ghp_super-secret-value-42") {
		t.Fatalf("doctor leaked a secret value:\n%s", stdout.String())
	}
}

func TestDoctor_LLMDownFails(t *testing.T) {
	env := seedDoctorEnv(t, "http://127.0.0.1:1")

	var stdout, stderr bytes.Buffer
	code := runDoctorCmd(nil, env, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runDoctorCmd = %d, want 1; stdout:\n%s", code, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"query embed dim: FAILED", "index embed dim: FAILED", "chat: FAILED", "doctor: problems found"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q in:\n%s", want, out)
		}
	}
}

func TestDoctor_StaleThresholdRespectsEnv(t *testing.T) {
	srv := fakeLLMServer(t)
	env := seedDoctorEnv(t, srv.URL)
	env.StaleAfter = 48 * time.Hour

	var stdout, stderr bytes.Buffer
	code := runDoctorCmd(nil, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runDoctorCmd = %d, stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "stale threshold 48h0m0s") {
		t.Errorf("stdout missing stale threshold marker:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "  - my-gh"):
			if !strings.Contains(line, "(fresh)") {
				t.Errorf("my-gh should be fresh with a 48h threshold (30h ago), line: %q", line)
			}
		case strings.HasPrefix(line, "  - stale-wiki"):
			if !strings.Contains(line, "(stale)") {
				t.Errorf("stale-wiki should be stale with a 48h threshold (60h ago), line: %q", line)
			}
		}
	}
}

func TestDoctor_FlagParsing(t *testing.T) {
	srv := fakeLLMServer(t)
	env := seedDoctorEnv(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runDoctorCmd([]string{"--bogus"}, env, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("runDoctorCmd(--bogus) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Errorf("stderr = %q, want flag error", stderr.String())
	}
}

func TestDoctor_MissingRootIsInformational(t *testing.T) {
	srv := fakeLLMServer(t)
	env := config.Defaults()
	env.KBRoot = t.TempDir()
	env.PersistDir = filepath.Join(t.TempDir(), "persist")
	env.LLMBaseURL = srv.URL
	env.EmbedBaseURL = srv.URL
	env.EmbedIndexBaseURL = srv.URL
	env.NoProxy = []string{"127.0.0.1"}

	var stdout, stderr bytes.Buffer
	code := runDoctorCmd(nil, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runDoctorCmd = %d, want 0 (missing root is informational); stdout:\n%s", code, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"no index yet", "sources: none"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q in:\n%s", want, out)
		}
	}
}
