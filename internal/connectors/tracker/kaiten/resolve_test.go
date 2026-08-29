package kaiten

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
	if got := New().Type(); got != "kaiten" {
		t.Fatalf("Type() = %q, want kaiten", got)
	}
}

func TestResolve_RequiresBaseURL(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "kt", Secrets: map[string]string{"token": "TOK"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"TOK": "x"}))
	if err == nil {
		t.Fatal("expected error when config.base_url is missing")
	}
}

func TestResolve_RequiresToken(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "kt", Config: map[string]string{"base_url": "https://kt.example.com"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when secrets.token is missing")
	}
}

func TestResolve_DefaultsAndTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "kt",
		Config:  map[string]string{"base_url": "https://kt.example.com"},
		Secrets: map[string]string{"token": "KAITEN_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"KAITEN_TOKEN": "secret-bearer"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.webBase != "https://kt.example.com" {
		t.Errorf("webBase = %q", c.webBase)
	}
	if c.token != "secret-bearer" {
		t.Errorf("token = %q, want secret-bearer", c.token)
	}
}
