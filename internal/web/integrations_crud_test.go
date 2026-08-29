package web

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
)

func writeSourcesFileContent(t *testing.T, te *testEnv, content string) {
	t.Helper()
	if err := os.WriteFile(te.server.deps.SourcesPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write sources.yaml: %v", err)
	}
}

func readSourcesFileContent(t *testing.T, te *testEnv) string {
	t.Helper()
	data, err := os.ReadFile(te.server.deps.SourcesPath)
	if err != nil {
		t.Fatalf("read sources.yaml: %v", err)
	}
	return string(data)
}

func TestIntegrations_AddSource(t *testing.T) {
	te := newTestEnv(t, nil)
	form := url.Values{
		"name":    {"leon-code"},
		"type":    {"file"},
		"config":  {"path=/tmp/leon-src\ninclude=*.go"},
		"secrets": {"token=KB_DISCORD_TOKEN"},
	}
	rr := postForm(t, te.server.Handler(), "/integrations/save", form)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusSeeOther, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/integrations" {
		t.Fatalf("Location = %q, want /integrations", loc)
	}

	cfg, err := config.LoadSourcesFile(te.server.deps.SourcesPath)
	if err != nil {
		t.Fatalf("LoadSourcesFile: %v", err)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(cfg.Sources))
	}
	src := cfg.Sources[0]
	if src.Name != "leon-code" || src.Type != "file" {
		t.Fatalf("source = %#v", src)
	}
	if src.Config["path"] != "/tmp/leon-src" || src.Config["include"] != "*.go" {
		t.Fatalf("config = %#v", src.Config)
	}
	if src.Secrets["token"] != "KB_DISCORD_TOKEN" {
		t.Fatalf("secrets = %#v, want env var name", src.Secrets)
	}

	body := getPage(t, te.server.Handler(), "/integrations").Body.String()
	for _, want := range []string{"leon-code", "file", "KB_DISCORD_TOKEN", "path", "include"} {
		if !strings.Contains(body, want) {
			t.Errorf("integrations page missing %q", want)
		}
	}
}

func TestIntegrations_AddSourceDuplicateName(t *testing.T) {
	te := newTestEnv(t, nil)
	writeSourcesFileContent(t, te, `
sources:
  - name: dup
    type: github
    config:
      repo: old
`)
	before := readSourcesFileContent(t, te)

	form := url.Values{"name": {"dup"}, "type": {"github"}, "config": {"repo=new"}}
	rr := postForm(t, te.server.Handler(), "/integrations/save", form)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "names must be unique") && !strings.Contains(rr.Body.String(), "duplicate") {
		t.Errorf("expected duplicate error, got %q", rr.Body.String())
	}
	if after := readSourcesFileContent(t, te); after != before {
		t.Errorf("sources.yaml changed after failed add:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestIntegrations_AddSourceSecretNotEnvName(t *testing.T) {
	te := newTestEnv(t, nil)
	form := url.Values{"name": {"bad"}, "type": {"github"}, "secrets": {"token=mysecret"}}
	rr := postForm(t, te.server.Handler(), "/integrations/save", form)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "env var NAME") {
		t.Errorf("expected env var name error, got %q", rr.Body.String())
	}
	if _, err := os.Stat(te.server.deps.SourcesPath); !os.IsNotExist(err) {
		t.Errorf("sources.yaml should not be created after failed save")
	}
}

func TestIntegrations_AddSourceInvalidConfigLine(t *testing.T) {
	te := newTestEnv(t, nil)
	form := url.Values{"name": {"bad"}, "type": {"github"}, "config": {"not-a-key-value"}}
	rr := postForm(t, te.server.Handler(), "/integrations/save", form)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "expected key=value") {
		t.Errorf("expected key=value error, got %q", rr.Body.String())
	}
}

func TestIntegrations_AddSourceMissingName(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/integrations/save", url.Values{"type": {"github"}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rr.Body.String(), "name is required") {
		t.Errorf("expected name required, got %q", rr.Body.String())
	}
}

func TestIntegrations_AddSourceUnknownType(t *testing.T) {
	te := newTestEnv(t, nil)
	form := url.Values{"name": {"bad"}, "type": {"does-not-exist"}, "config": {"x=y"}}
	rr := postForm(t, te.server.Handler(), "/integrations/save", form)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unknown connector type") {
		t.Errorf("expected unknown connector type error, got %q", rr.Body.String())
	}
	if _, err := os.Stat(te.server.deps.SourcesPath); !os.IsNotExist(err) {
		t.Errorf("sources.yaml should not be created after failed save")
	}
}

func TestIntegrations_EditSource(t *testing.T) {
	te := newTestEnv(t, nil)
	writeSourcesFileContent(t, te, `
sources:
  - name: editme
    type: github
    config:
      repo: old
    secrets:
      token: GITHUB_TOKEN
`)
	form := url.Values{
		"original": {"editme"},
		"name":     {"editme"},
		"type":     {"github"},
		"config":   {"repo=new"},
		"secrets":  {"token=KB_DISCORD_TOKEN"},
	}
	rr := postForm(t, te.server.Handler(), "/integrations/save", form)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusSeeOther, rr.Body.String())
	}

	cfg, err := config.LoadSourcesFile(te.server.deps.SourcesPath)
	if err != nil {
		t.Fatalf("LoadSourcesFile: %v", err)
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(cfg.Sources))
	}
	src := cfg.Sources[0]
	if src.Config["repo"] != "new" {
		t.Fatalf("config = %#v, want repo=new", src.Config)
	}
	if src.Secrets["token"] != "KB_DISCORD_TOKEN" {
		t.Fatalf("secrets = %#v, want KB_DISCORD_TOKEN", src.Secrets)
	}
}

func TestIntegrations_EditSourceMissing(t *testing.T) {
	te := newTestEnv(t, nil)
	form := url.Values{"original": {"ghost"}, "name": {"ghost"}, "type": {"github"}}
	rr := postForm(t, te.server.Handler(), "/integrations/save", form)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	if !strings.Contains(rr.Body.String(), "source not found: ghost") {
		t.Errorf("expected not found error, got %q", rr.Body.String())
	}
}

func TestIntegrations_EditSourceRejectsRename(t *testing.T) {
	te := newTestEnv(t, nil)
	writeSourcesFileContent(t, te, `
sources:
  - name: editme
    type: github
    config:
      repo: old
`)
	before := readSourcesFileContent(t, te)

	form := url.Values{"original": {"editme"}, "name": {"renamed"}, "type": {"github"}, "config": {"repo=new"}}
	rr := postForm(t, te.server.Handler(), "/integrations/save", form)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "renaming a source is not supported") {
		t.Errorf("expected rename rejection, got %q", rr.Body.String())
	}
	if after := readSourcesFileContent(t, te); after != before {
		t.Errorf("sources.yaml changed after failed rename:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestIntegrations_EditSourceRejectsTypeChange(t *testing.T) {
	te := newTestEnv(t, nil)
	writeSourcesFileContent(t, te, `
sources:
  - name: editme
    type: github
    config:
      repo: old
`)
	before := readSourcesFileContent(t, te)

	form := url.Values{"original": {"editme"}, "name": {"editme"}, "type": {"file"}, "config": {"path=/tmp/x"}}
	rr := postForm(t, te.server.Handler(), "/integrations/save", form)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "changing a source type is not supported") {
		t.Errorf("expected type-change rejection, got %q", rr.Body.String())
	}
	if after := readSourcesFileContent(t, te); after != before {
		t.Errorf("sources.yaml changed after failed type change:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestIntegrations_DeleteSource(t *testing.T) {
	te := newTestEnv(t, nil)
	writeSourcesFileContent(t, te, `
sources:
  - name: keep
    type: github
    config:
      repo: keep
  - name: gone
    type: file
    config:
      path: /tmp/gone
`)
	writeDoc(t, te.root, "gone/one.md", connector.Document{
		ID: "one", Source: "gone", Title: "one", Body: "body",
	})

	rr := postForm(t, te.server.Handler(), "/integrations/delete", url.Values{"name": {"gone"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusSeeOther, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/integrations?deleted=gone&related=1") {
		t.Fatalf("Location = %q, want deleted=gone&related=1", loc)
	}

	cfg, err := config.LoadSourcesFile(te.server.deps.SourcesPath)
	if err != nil {
		t.Fatalf("LoadSourcesFile: %v", err)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].Name != "keep" {
		t.Fatalf("sources = %#v, want only keep", cfg.Sources)
	}

	body := getPage(t, te.server.Handler(), loc).Body.String()
	if !strings.Contains(body, "source gone removed from sources.yaml") {
		t.Errorf("expected delete confirmation, got %q", body)
	}
	if !strings.Contains(body, "1 document(s) in the corpus still reference it") {
		t.Errorf("expected related-documents warning, got %q", body)
	}
}

func TestIntegrations_DeleteSourceReferencedByVirtualCollectionFails(t *testing.T) {
	te := newTestEnv(t, nil)
	writeSourcesFileContent(t, te, `
sources:
  - name: gone
    type: file
    config:
      path: /tmp/gone
virtual_collections:
  main-chat:
    - file:gone
`)
	before := readSourcesFileContent(t, te)

	rr := postForm(t, te.server.Handler(), "/integrations/delete", url.Values{"name": {"gone"}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "references unknown source") {
		t.Errorf("expected dangling collection reference error, got %q", rr.Body.String())
	}
	if after := readSourcesFileContent(t, te); after != before {
		t.Errorf("sources.yaml changed after failed delete:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestIntegrations_DeleteSourceMissing(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/integrations/delete", url.Values{"name": {"ghost"}})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}
