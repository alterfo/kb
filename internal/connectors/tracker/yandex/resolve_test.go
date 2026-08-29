package yandex

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
	if got := New().Type(); got != "yandex-tracker" {
		t.Fatalf("Type() = %q, want yandex-tracker", got)
	}
}

func TestResolve_RequiresOrgID(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "yt", Config: map[string]string{"queues": "KB"}, Secrets: map[string]string{"token": "TOK"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"TOK": "x"}))
	if err == nil {
		t.Fatal("expected error when config.org_id is missing")
	}
}

func TestResolve_RequiresQueues(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "yt", Config: map[string]string{"org_id": "123"}, Secrets: map[string]string{"token": "TOK"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"TOK": "x"}))
	if err == nil {
		t.Fatal("expected error when config.queues is missing")
	}
}

func TestResolve_RequiresToken(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "yt", Config: map[string]string{"org_id": "123", "queues": "KB"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when secrets.token is missing")
	}
}

func TestResolve_DefaultsAndTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "yt",
		Config:  map[string]string{"org_id": "123", "queues": "KB, OPS"},
		Secrets: map[string]string{"token": "YT_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"YT_TOKEN": "secret-oauth"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://api.tracker.yandex.net" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
	if c.webBase != "https://tracker.yandex.ru" {
		t.Errorf("webBase = %q", c.webBase)
	}
	if c.token != "secret-oauth" {
		t.Errorf("token = %q, want secret-oauth", c.token)
	}
	if len(c.queues) != 2 || c.queues[0] != "KB" || c.queues[1] != "OPS" {
		t.Fatalf("queues = %v", c.queues)
	}
}
