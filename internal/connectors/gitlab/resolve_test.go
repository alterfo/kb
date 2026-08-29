package gitlab

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
	if got := New().Type(); got != "gitlab" {
		t.Fatalf("Type() = %q, want gitlab", got)
	}
}

func TestResolve_RequiresGroupOrProjects(t *testing.T) {
	c := New()
	err := c.Resolve(context.Background(), connector.Config{Name: "gl"}, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when neither group nor projects configured")
	}
}

func TestResolve_DefaultsAndTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "main-group",
		Config:  map[string]string{"group": "acme"},
		Secrets: map[string]string{"token": "GITLAB_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"GITLAB_TOKEN": "secret-pat"}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://gitlab.com/api/v4" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
	if c.webBase != "https://gitlab.com" {
		t.Errorf("webBase = %q", c.webBase)
	}
	if !c.includeWiki || !c.includeFiles {
		t.Errorf("includeWiki/includeFiles should default true")
	}
	if c.token != "secret-pat" {
		t.Errorf("token = %q, want secret-pat", c.token)
	}
}

func TestResolve_MissingSecretEnvIsFailOpen(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "main-group",
		Config:  map[string]string{"group": "acme"},
		Secrets: map[string]string{"token": "GITLAB_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.token != "" {
		t.Errorf("token = %q, want empty (unauthenticated public access)", c.token)
	}
}

func TestResolve_ExplicitProjectList(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:   "main-group",
		Config: map[string]string{"projects": "acme/widgets, acme/gizmos"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(c.projects) != 2 || c.projects[0] != "acme/widgets" || c.projects[1] != "acme/gizmos" {
		t.Fatalf("projects = %v", c.projects)
	}
}

func TestResolve_DisableWikiAndFiles(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name: "main-group",
		Config: map[string]string{
			"group":         "acme",
			"include_wiki":  "false",
			"include_files": "false",
		},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.includeWiki || c.includeFiles {
		t.Fatalf("includeWiki/includeFiles should be false")
	}
}
