package mcp

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
	if got := New().Type(); got != "mcp" {
		t.Fatalf("Type() = %q, want mcp", got)
	}
}

func TestResolve_RequiresValidTransport(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "m", Config: map[string]string{}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err == nil {
		t.Fatal("expected error when config.transport is missing")
	}

	c2 := New()
	cfg2 := connector.Config{Name: "m", Config: map[string]string{"transport": "carrier-pigeon"}}
	if err := c2.Resolve(context.Background(), cfg2, fakeEnv(nil)); err == nil {
		t.Fatal("expected error for unknown transport")
	}
}

func TestResolve_StdioRequiresCommand(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "m", Config: map[string]string{"transport": "stdio"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err == nil {
		t.Fatal("expected error when config.command is missing")
	}
}

func TestResolve_HTTPRequiresURL(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "m", Config: map[string]string{"transport": "http"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err == nil {
		t.Fatal("expected error when config.url is missing")
	}
}

func TestResolve_StdioOK(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "m", Config: map[string]string{"transport": "stdio", "command": "some-mcp-server --flag"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.transportKind != "stdio" {
		t.Errorf("transportKind = %q, want stdio", c.transportKind)
	}
	if c.command != "some-mcp-server --flag" {
		t.Errorf("command = %q", c.command)
	}
}

func TestResolve_HTTPTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "m",
		Config:  map[string]string{"transport": "http", "url": "https://mcp.example/mcp"},
		Secrets: map[string]string{"token": "MCP_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"MCP_TOKEN": "secret-token"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.url != "https://mcp.example/mcp" {
		t.Errorf("url = %q", c.url)
	}
	if c.token != "secret-token" {
		t.Errorf("token = %q, want secret-token", c.token)
	}
}

func TestResolve_MissingSecretEnvIsFailOpen(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "m",
		Config:  map[string]string{"transport": "http", "url": "https://mcp.example/mcp"},
		Secrets: map[string]string{"token": "MCP_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.token != "" {
		t.Errorf("token = %q, want empty (unauthenticated)", c.token)
	}
}
