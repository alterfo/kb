package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestListSources_NoFileReturnsEmpty(t *testing.T) {
	te := newTestEnv(t, nil)
	_, out, err := te.server.listSources(context.Background(), nil, listSourcesIn{})
	if err != nil {
		t.Fatalf("listSources: %v", err)
	}
	if len(out.Sources) != 0 {
		t.Fatalf("listSources: got %d sources, want 0", len(out.Sources))
	}
}

func TestAddSourceThenListSources_RoundTripsWithoutLeakingSecretValues(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()

	_, addOut, err := te.server.addSource(ctx, nil, addSourceIn{
		Name:    "myrepo",
		Type:    "github",
		Config:  map[string]string{"repo": "owner/name"},
		Secrets: map[string]string{"token": "GITHUB_TOKEN"},
	})
	if err != nil {
		t.Fatalf("addSource: %v", err)
	}
	if addOut.Name != "myrepo" || addOut.Type != "github" {
		t.Fatalf("addSource: out = %+v", addOut)
	}

	data, err := os.ReadFile(te.server.deps.SourcesPath)
	if err != nil {
		t.Fatalf("read sources.yaml: %v", err)
	}
	if !strings.Contains(string(data), "GITHUB_TOKEN") {
		t.Fatalf("sources.yaml should contain the env var name, got:\n%s", data)
	}

	_, listOut, err := te.server.listSources(ctx, nil, listSourcesIn{})
	if err != nil {
		t.Fatalf("listSources: %v", err)
	}
	if len(listOut.Sources) != 1 {
		t.Fatalf("listSources: got %d sources, want 1", len(listOut.Sources))
	}
	got := listOut.Sources[0]
	if got.Name != "myrepo" || got.Type != "github" {
		t.Fatalf("listSources: source = %+v", got)
	}
	if len(got.SecretEnvs) != 1 || got.SecretEnvs[0] != "GITHUB_TOKEN" {
		t.Fatalf("listSources: SecretEnvs = %+v, want [GITHUB_TOKEN]", got.SecretEnvs)
	}
}

func TestAddSource_RejectsLiteralSecretValue(t *testing.T) {
	te := newTestEnv(t, nil)
	_, _, err := te.server.addSource(context.Background(), nil, addSourceIn{
		Name:    "myrepo",
		Type:    "github",
		Secrets: map[string]string{"token": "literal_secret_token"},
	})
	if err == nil {
		t.Fatalf("addSource: got nil error for literal secret value, want rejection")
	}
}

func TestAddSource_DuplicateInstanceRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	in := addSourceIn{Name: "myrepo", Type: "github"}
	if _, _, err := te.server.addSource(ctx, nil, in); err != nil {
		t.Fatalf("addSource[0]: %v", err)
	}
	if _, _, err := te.server.addSource(ctx, nil, in); err == nil {
		t.Fatalf("addSource[1]: got nil error for duplicate instance, want rejection")
	}
}
