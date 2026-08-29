package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegrations_PresenceOnlyStalenessAndErrors(t *testing.T) {
	root := t.TempDir()
	persist := filepath.Join(root, ".persist")
	if err := os.MkdirAll(persist, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcesPath := filepath.Join(root, "sources.yaml")
	statePath := filepath.Join(persist, ".sync-state.json")

	if err := os.WriteFile(sourcesPath, []byte(`
sources:
  - name: my-gh
    type: github
    config:
      repo: myrepo
    secrets:
      token: GITHUB_TOKEN
  - name: stale-wiki
    type: wiki
    config:
      space: team
    secrets:
      token: WIKI_TOKEN
virtual_collections:
  code:
    - github
`), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	fresh := now.Add(-1 * time.Hour)
	state := fmt.Sprintf(`{"sources":{
		"github:my-gh": {"cursor":"c1","last_sync_at":%q},
		"wiki:stale-wiki": {"cursor":"c2","last_sync_at":%q,"last_error":"sync boom"}
	}}`, fresh.Format(time.RFC3339), old.Format(time.RFC3339))
	if err := os.WriteFile(statePath, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(Deps{
		Root:        root,
		PersistDir:  persist,
		SourcesPath: sourcesPath,
		StatePath:   statePath,
		StaleAfter:  24 * time.Hour,
		Now:         func() time.Time { return now },
		EnvLookup: func(key string) (string, bool) {
			if key == "GITHUB_TOKEN" {
				return "supersecret123", true
			}
			return "", false
		},
	})

	rr := getPage(t, srv.Handler(), "/integrations")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"my-gh", "stale-wiki", "GITHUB_TOKEN", "WIKI_TOKEN", "badge-stale", "badge-ok", "sync boom", "code"} {
		if !strings.Contains(body, want) {
			t.Errorf("integrations page missing %q", want)
		}
	}
	if strings.Contains(body, "supersecret123") {
		t.Error("secret VALUE leaked into page")
	}
	if strings.Contains(body, "myrepo") {
		t.Error("config VALUE leaked into page")
	}
	if !strings.Contains(body, "(unset)") {
		t.Errorf("expected unset env marker for WIKI_TOKEN")
	}
}

func TestIntegrations_NoSourcesFileShowsNote(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/integrations")
	body := rr.Body.String()
	if !strings.Contains(body, "no sources configured") {
		t.Errorf("expected empty integrations table, got %q", body)
	}
}
