package slack

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
	if got := New().Type(); got != "slack" {
		t.Fatalf("Type() = %q, want slack", got)
	}
}

func TestResolve_RequiresChannels(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "sl", Secrets: map[string]string{"token": "SLACK_TOKEN"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"SLACK_TOKEN": "xoxb-1"}))
	if err == nil {
		t.Fatal("expected error when config.channels is missing")
	}
}

func TestResolve_RequiresToken(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "sl", Config: map[string]string{"channels": "C1"}}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when secrets.token is missing")
	}
}

func TestResolve_DefaultsAndChannelList(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "sl",
		Config:  map[string]string{"channels": "C1, C2"},
		Secrets: map[string]string{"token": "SLACK_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"SLACK_TOKEN": "xoxb-1"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://slack.com/api" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
	if len(c.channels) != 2 || c.channels[0] != "C1" || c.channels[1] != "C2" {
		t.Fatalf("channels = %v", c.channels)
	}
	if c.token != "xoxb-1" {
		t.Errorf("token = %q, want xoxb-1", c.token)
	}
}
