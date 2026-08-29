package youtrack

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
	if got := New().Type(); got != "youtrack" {
		t.Fatalf("Type() = %q, want youtrack", got)
	}
}

func TestResolve_RequiresBaseURL(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "yt", Secrets: map[string]string{"token": "TOK"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"TOK": "x"}))
	if err == nil {
		t.Fatal("expected error when config.base_url is missing")
	}
}

func TestResolve_RequiresToken(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "yt", Config: map[string]string{"base_url": "https://yt.example.com"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when secrets.token is missing")
	}
}

func TestResolve_DefaultsAndTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "yt",
		Config:  map[string]string{"base_url": "https://yt.example.com"},
		Secrets: map[string]string{"token": "YT_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"YT_TOKEN": "secret-perm-token"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.webBase != "https://yt.example.com" {
		t.Errorf("webBase = %q", c.webBase)
	}
	if c.token != "secret-perm-token" {
		t.Errorf("token = %q, want secret-perm-token", c.token)
	}
}

func TestResolve_ExplicitWebBaseURL(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "yt",
		Config:  map[string]string{"base_url": "https://yt.example.com", "web_base_url": "https://yt.example.com/youtrack"},
		Secrets: map[string]string{"token": "YT_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"YT_TOKEN": "x"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.webBase != "https://yt.example.com/youtrack" {
		t.Errorf("webBase = %q", c.webBase)
	}
}
