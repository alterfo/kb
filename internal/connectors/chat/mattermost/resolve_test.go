package mattermost

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
	if got := New().Type(); got != "mattermost" {
		t.Fatalf("Type() = %q, want mattermost", got)
	}
}

func TestResolve_RequiresBaseURL(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "mm",
		Config:  map[string]string{"channels": "C1"},
		Secrets: map[string]string{"token": "MM_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"MM_TOKEN": "t"}))
	if err == nil {
		t.Fatal("expected error when config.base_url is missing")
	}
}

func TestResolve_RequiresChannels(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "mm",
		Config:  map[string]string{"base_url": "https://mm.example"},
		Secrets: map[string]string{"token": "MM_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"MM_TOKEN": "t"}))
	if err == nil {
		t.Fatal("expected error when config.channels is missing")
	}
}

func TestResolve_RequiresToken(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:   "mm",
		Config: map[string]string{"base_url": "https://mm.example", "channels": "C1"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when secrets.token is missing")
	}
}

func TestResolve_DefaultsAndTeam(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name: "mm",
		Config: map[string]string{
			"base_url": "https://mm.example/",
			"channels": "C1, C2",
			"team":     "acme",
		},
		Secrets: map[string]string{"token": "MM_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"MM_TOKEN": "t"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://mm.example" {
		t.Errorf("apiBase = %q, want trimmed trailing slash", c.apiBase)
	}
	if c.webBase != "https://mm.example" {
		t.Errorf("webBase = %q, want default to apiBase", c.webBase)
	}
	if c.team != "acme" {
		t.Errorf("team = %q, want acme", c.team)
	}
	if len(c.channels) != 2 || c.channels[0] != "C1" || c.channels[1] != "C2" {
		t.Fatalf("channels = %v", c.channels)
	}
	if c.token != "t" {
		t.Errorf("token = %q, want t", c.token)
	}
}
