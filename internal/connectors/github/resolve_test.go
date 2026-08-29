package github

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

func fakeEnv(m map[string]string) connector.EnvLookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestType(t *testing.T) {
	if got := New().Type(); got != "github" {
		t.Fatalf("Type() = %q, want github", got)
	}
}

func TestResolve_RequiresOrgOrRepos(t *testing.T) {
	c := New()
	err := c.Resolve(context.Background(), connector.Config{Name: "gh"}, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when neither org nor repos configured")
	}
}

func TestResolve_DefaultsAndTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "main-org",
		Config:  map[string]string{"org": "acme"},
		Secrets: map[string]string{"token": "GITHUB_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"GITHUB_TOKEN": "secret-pat"}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://api.github.com" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
	if c.webBase != "https://github.com" {
		t.Errorf("webBase = %q", c.webBase)
	}
	if c.rawBase != "https://raw.githubusercontent.com" {
		t.Errorf("rawBase = %q", c.rawBase)
	}
	if !c.includeWiki || !c.includeContents {
		t.Errorf("includeWiki/includeContents should default true")
	}
	if c.token != "secret-pat" {
		t.Errorf("token = %q, want secret-pat", c.token)
	}
}

func TestResolve_MissingSecretEnvIsFailOpen(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "main-org",
		Config:  map[string]string{"org": "acme"},
		Secrets: map[string]string{"token": "GITHUB_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.token != "" {
		t.Errorf("token = %q, want empty (unauthenticated public access)", c.token)
	}
}

func TestResolve_ExplicitRepoList(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:   "main-org",
		Config: map[string]string{"repos": "acme/widgets, acme/gizmos"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(c.repos) != 2 || c.repos[0] != "acme/widgets" || c.repos[1] != "acme/gizmos" {
		t.Fatalf("repos = %v", c.repos)
	}
}

func TestResolve_DisableWikiAndContents(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name: "main-org",
		Config: map[string]string{
			"org":              "acme",
			"include_wiki":     "false",
			"include_contents": "false",
		},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.includeWiki || c.includeContents {
		t.Fatalf("includeWiki/includeContents should be false")
	}
}
