package telegram

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
	if got := New().Type(); got != "telegram" {
		t.Fatalf("Type() = %q, want telegram", got)
	}
}

func TestResolve_RequiresToken(t *testing.T) {
	c := New()
	err := c.Resolve(context.Background(), connector.Config{Name: "tg"}, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when secrets.token is missing")
	}
}

func TestResolve_MissingSecretEnvIsError(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "tg",
		Secrets: map[string]string{"token": "TG_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when TG_TOKEN env is not set")
	}
}

func TestResolve_DefaultsAndTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "tg",
		Secrets: map[string]string{"token": "TG_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"TG_TOKEN": "123:abc"}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://api.telegram.org" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
	if c.token != "123:abc" {
		t.Errorf("token = %q, want 123:abc", c.token)
	}
}

func TestResolve_CustomBaseURL(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "tg",
		Config:  map[string]string{"base_url": "http://localhost:9999/"},
		Secrets: map[string]string{"token": "TG_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"TG_TOKEN": "t"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "http://localhost:9999" {
		t.Errorf("apiBase = %q, want trimmed trailing slash", c.apiBase)
	}
}
