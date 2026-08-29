package discord

import (
	"context"
	"net/http"
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
	if got := New().Type(); got != "discord" {
		t.Fatalf("Type() = %q, want discord", got)
	}
}

func TestResolve_RequiresChannels(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "ds", Secrets: map[string]string{"token": "DISCORD_TOKEN"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"DISCORD_TOKEN": "t"}))
	if err == nil {
		t.Fatal("expected error when config.channels is missing")
	}
}

func TestResolve_RequiresToken(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "ds", Config: map[string]string{"channels": "C1"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when secrets.token is missing")
	}
}

func TestResolve_MissingSecretEnvIsError(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "ds", Config: map[string]string{"channels": "C1"}, Secrets: map[string]string{"token": "DISCORD_TOKEN"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when DISCORD_TOKEN env is not set")
	}
}

func TestResolve_DefaultsAndChannelList(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "ds",
		Config:  map[string]string{"channels": "C1, C2", "guild_id": "G1", "base_url": "https://discord.example/api/v10/"},
		Secrets: map[string]string{"token": "DISCORD_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"DISCORD_TOKEN": "bot-token"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://discord.example/api/v10" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
	if c.webBase != "https://discord.com" {
		t.Errorf("webBase = %q", c.webBase)
	}
	if c.guildID != "G1" {
		t.Errorf("guildID = %q", c.guildID)
	}
	if len(c.channels) != 2 || c.channels[0] != "C1" || c.channels[1] != "C2" {
		t.Fatalf("channels = %v", c.channels)
	}
	if c.token != "bot-token" {
		t.Errorf("token = %q", c.token)
	}
	if c.client == nil {
		t.Fatal("client = nil, want configured")
	}
}

func TestResolve_ConfiguresSOCKSProxy(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "ds",
		Config:  map[string]string{"channels": "C1"},
		Secrets: map[string]string{"token": "DISCORD_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{
		"DISCORD_TOKEN":  "bot-token",
		"KB_SOCKS_PROXY": "socks5://127.0.0.1:3333",
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.client == nil {
		t.Fatal("client = nil, want configured")
	}
	hc, ok := c.client.Doer().(*http.Client)
	if !ok {
		t.Fatalf("doer = %T, want *http.Client", c.client.Doer())
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", hc.Transport)
	}
	if tr.DialContext == nil {
		t.Fatal("SOCKS DialContext not installed on resolved transport")
	}
	if tr.Proxy != nil {
		t.Fatal("HTTP proxy should be disabled when SOCKS DialContext is configured")
	}
}

func TestResolve_InvalidSOCKSProxyIsError(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "ds",
		Config:  map[string]string{"channels": "C1"},
		Secrets: map[string]string{"token": "DISCORD_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{
		"DISCORD_TOKEN":  "bot-token",
		"KB_SOCKS_PROXY": "http://127.0.0.1:3333",
	}))
	if err == nil {
		t.Fatal("expected error for non-SOCKS KB_SOCKS_PROXY")
	}
}
