package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
)

type cmdFakeConnector struct{}

var cmdFakeOnce sync.Once

func registerCmdFake() {
	cmdFakeOnce.Do(func() {
		registry.Register("cmdtest-source", func() connector.Connector { return cmdFakeConnector{} })
	})
}

func (cmdFakeConnector) Type() string { return "cmdtest-source" }

func (cmdFakeConnector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	return nil
}

func (cmdFakeConnector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)
	out <- connector.Document{ID: "doc-1", Source: "cmdtest", Title: "Hello", Body: "world"}
	return connector.Cursor{Value: "cursor-1"}, connector.FetchInfo{ItemCount: 1}, nil
}

func TestRunSyncCmd_WritesDocumentsAndAdvancesState(t *testing.T) {
	registerCmdFake()

	root := t.TempDir()
	sourcesYAML := "sources:\n  - name: cmdtest\n    type: cmdtest-source\n"
	if err := os.WriteFile(filepath.Join(root, "sources.yaml"), []byte(sourcesYAML), 0o644); err != nil {
		t.Fatalf("writing sources.yaml: %v", err)
	}

	env := config.Defaults()
	env.KBRoot = root
	env.PersistDir = filepath.Join(root, ".persist")

	var stdout, stderr bytes.Buffer
	code := runSyncCmd([]string{"--all"}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runSyncCmd = %d, stderr = %s", code, stderr.String())
	}

	docPath := filepath.Join(root, "cmdtest", "doc-1.md")
	if _, err := os.Stat(docPath); err != nil {
		t.Fatalf("expected document written at %s: %v", docPath, err)
	}

	statePath := filepath.Join(env.PersistDir, ".sync-state.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected sync state written at %s: %v", statePath, err)
	}
}

func TestRunSyncCmd_UnknownSourceNameReturnsNoop(t *testing.T) {
	root := t.TempDir()
	sourcesYAML := "sources:\n  - name: cmdtest\n    type: cmdtest-source\n"
	if err := os.WriteFile(filepath.Join(root, "sources.yaml"), []byte(sourcesYAML), 0o644); err != nil {
		t.Fatalf("writing sources.yaml: %v", err)
	}

	env := config.Defaults()
	env.KBRoot = root
	env.PersistDir = filepath.Join(root, ".persist")

	var stdout, stderr bytes.Buffer
	code := runSyncCmd([]string{"--source=does-not-exist"}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runSyncCmd = %d, stderr = %s", code, stderr.String())
	}
	if want := "no matching sources"; !bytes.Contains(stdout.Bytes(), []byte(want)) {
		t.Fatalf("stdout = %q, want to contain %q", stdout.String(), want)
	}
}

func TestRunSyncCmd_APISinkPushesDocumentsInsteadOfFiles(t *testing.T) {
	registerCmdFake()

	var pushed []byte
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		pushed = append(pushed, body...)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer apiSrv.Close()

	root := t.TempDir()
	sourcesYAML := "sources:\n  - name: cmdtest\n    type: cmdtest-source\n"
	if err := os.WriteFile(filepath.Join(root, "sources.yaml"), []byte(sourcesYAML), 0o644); err != nil {
		t.Fatalf("writing sources.yaml: %v", err)
	}

	env := config.Defaults()
	env.KBRoot = root
	env.PersistDir = filepath.Join(root, ".persist")

	var stdout, stderr bytes.Buffer
	code := runSyncCmd([]string{"--all", "--api=" + apiSrv.URL}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runSyncCmd = %d, stderr = %s", code, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(root, "cmdtest", "doc-1.md")); !os.IsNotExist(err) {
		t.Fatalf("API mode should not write files, stat err = %v", err)
	}
	if !bytes.Contains(pushed, []byte("doc-1")) {
		t.Fatalf("API sink received %q, want document id doc-1", pushed)
	}
}

func TestRunSyncCmd_FileConnectorIngestsFixture(t *testing.T) {
	fixtureDir, err := filepath.Abs(filepath.Join("testdata", "leon-repo"))
	if err != nil {
		t.Fatalf("resolving fixture path: %v", err)
	}

	root := t.TempDir()
	sourcesYAML := fmt.Sprintf("sources:\n  - name: leon-code\n    type: file\n    config:\n      path: %q\n", fixtureDir)
	if err := os.WriteFile(filepath.Join(root, "sources.yaml"), []byte(sourcesYAML), 0o644); err != nil {
		t.Fatalf("writing sources.yaml: %v", err)
	}

	env := config.Defaults()
	env.KBRoot = root
	env.PersistDir = filepath.Join(root, ".persist")

	var stdout, stderr bytes.Buffer
	code := runSyncCmd([]string{"--all"}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runSyncCmd = %d, stderr = %s", code, stderr.String())
	}

	docsDir := filepath.Join(root, "leon-code")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		t.Fatalf("reading leon-code corpus: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("leon-code docs = %d, want 2 fixture docs", len(entries))
	}

	var corpus strings.Builder
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(docsDir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		corpus.Write(data)
	}
	for _, want := range []string{"source: leon-code", "package main", "leon-fixture"} {
		if !strings.Contains(corpus.String(), want) {
			t.Fatalf("corpus missing %q:\n%s", want, corpus.String())
		}
	}

	if _, err := os.Stat(filepath.Join(env.PersistDir, ".sync-state.json")); err != nil {
		t.Fatalf("expected sync state written at %s: %v", env.PersistDir, err)
	}
}
