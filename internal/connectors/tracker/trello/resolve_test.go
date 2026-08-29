package trello

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
	if got := New().Type(); got != "trello" {
		t.Fatalf("Type() = %q, want trello", got)
	}
}

func TestResolve_PublicBoardWithoutSecrets(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:   "leon-trello",
		Config: map[string]string{"board_id": "7bdwhnLr"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.boardID != "7bdwhnLr" {
		t.Errorf("boardID = %q", c.boardID)
	}
	if c.apiBase != "https://api.trello.com" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
	if c.publicBase != "https://trello.com" {
		t.Errorf("publicBase = %q", c.publicBase)
	}
	if c.key != "" || c.token != "" {
		t.Errorf("key/token = %q/%q, want empty", c.key, c.token)
	}
}

func TestResolve_PrivateBoardWithKeyAndToken(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "private",
		Config:  map[string]string{"board_id": "abc"},
		Secrets: map[string]string{"key": "TRELLO_KEY", "token": "TRELLO_TOKEN"},
	}
	env := map[string]string{"TRELLO_KEY": "api-key", "TRELLO_TOKEN": "api-token"}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(env)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.key != "api-key" {
		t.Errorf("key = %q", c.key)
	}
	if c.token != "api-token" {
		t.Errorf("token = %q", c.token)
	}
}

func TestResolve_MissingBoardID(t *testing.T) {
	c := New()
	err := c.Resolve(context.Background(), connector.Config{Name: "empty"}, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when board_id is missing")
	}
}

func TestResolve_PartialSecrets(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "partial",
		Config:  map[string]string{"board_id": "abc"},
		Secrets: map[string]string{"key": "TRELLO_KEY"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"TRELLO_KEY": "x"}))
	if err == nil {
		t.Fatal("expected error when only one of key/token is configured")
	}
}

func TestResolve_MissingSecretEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "missing",
		Config:  map[string]string{"board_id": "abc"},
		Secrets: map[string]string{"key": "TRELLO_KEY", "token": "TRELLO_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when secret env is not set")
	}
}
