package searchapi

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
	if got := New().Type(); got != "searchapi" {
		t.Fatalf("Type() = %q, want searchapi", got)
	}
}

func TestResolve_RequiresSearchURL(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "sa"}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err == nil {
		t.Fatal("expected error when config.search_url is missing")
	}
}

func TestResolve_AuthNoneByDefault(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "sa", Config: map[string]string{"search_url": "https://example.com/search"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.auth.Kind != connector.AuthNone {
		t.Errorf("auth.Kind = %q, want none", c.auth.Kind)
	}
}

func TestResolve_UnknownAuthKind(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "sa", Config: map[string]string{
		"search_url": "https://example.com/search",
		"auth_kind":  "hmac",
	}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err == nil {
		t.Fatal("expected error for unknown auth_kind")
	}
}

func TestResolve_BearerRequiresToken(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "sa", Config: map[string]string{
		"search_url": "https://example.com/search",
		"auth_kind":  "bearer",
	}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err == nil {
		t.Fatal("expected error when secrets.token missing for bearer auth")
	}
}

func TestResolve_BearerTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name: "sa",
		Config: map[string]string{
			"search_url": "https://example.com/search",
			"auth_kind":  "bearer",
		},
		Secrets: map[string]string{"token": "SA_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"SA_TOKEN": "secret"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.auth.Token != "secret" {
		t.Errorf("auth.Token = %q, want secret", c.auth.Token)
	}
}

func TestResolve_BasicRequiresUsernameAndPassword(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "sa", Config: map[string]string{
		"search_url": "https://example.com/search",
		"auth_kind":  "basic",
	}, Secrets: map[string]string{"username": "U", "password": "P"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"U": "bob"})); err == nil {
		t.Fatal("expected error when secrets.password missing for basic auth")
	}
}

func TestResolve_APIKeyDefaultsHeader(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name: "sa",
		Config: map[string]string{
			"search_url": "https://example.com/search",
			"auth_kind":  "apikey",
		},
		Secrets: map[string]string{"token": "SA_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"SA_TOKEN": "secret"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.auth.Header != "X-Api-Key" {
		t.Errorf("auth.Header = %q, want X-Api-Key", c.auth.Header)
	}
}

func TestResolve_FieldMapExtraFromFmPrefix(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "sa", Config: map[string]string{
		"search_url": "https://example.com/search",
		"fm_status":  "state",
		"fm_author":  "author.name",
	}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.fields.Extra["status"] != "state" || c.fields.Extra["author"] != "author.name" {
		t.Errorf("fields.Extra = %+v", c.fields.Extra)
	}
}

func TestResolve_RejectsNonPositivePagerStep(t *testing.T) {
	for _, step := range []string{"0s", "-1h"} {
		c := New()
		cfg := connector.Config{Name: "sa", Config: map[string]string{
			"search_url": "https://example.com/search",
			"pager_step": step,
		}}
		if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err == nil {
			t.Fatalf("Resolve accepted non-positive pager_step %q", step)
		}
	}
}
