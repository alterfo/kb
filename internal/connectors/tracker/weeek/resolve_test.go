package weeek

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
	if got := New().Type(); got != "weeek" {
		t.Fatalf("Type() = %q, want weeek", got)
	}
}

func TestResolve_RequiresToken(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "wk"}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when secrets.token is missing")
	}
}

func TestResolve_DefaultsAndTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "wk",
		Secrets: map[string]string{"token": "WEEEK_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"WEEEK_TOKEN": "secret-bearer"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://api.weeek.net" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
	if c.webBase != "https://app.weeek.net" {
		t.Errorf("webBase = %q", c.webBase)
	}
	if c.token != "secret-bearer" {
		t.Errorf("token = %q, want secret-bearer", c.token)
	}
}

func TestResolve_ExplicitBaseURL(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "wk",
		Config:  map[string]string{"base_url": "https://wk.example.com", "web_base_url": "https://wk.example.com/app"},
		Secrets: map[string]string{"token": "WEEEK_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"WEEEK_TOKEN": "x"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://wk.example.com" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
	if c.webBase != "https://wk.example.com/app" {
		t.Errorf("webBase = %q", c.webBase)
	}
}
