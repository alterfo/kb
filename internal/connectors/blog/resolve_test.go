package blog

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
	if got := New().Type(); got != "rss" {
		t.Fatalf("Type() = %q, want rss", got)
	}
}

func TestResolve_RequiresFeedURL(t *testing.T) {
	c := New()
	err := c.Resolve(context.Background(), connector.Config{Name: "blog"}, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when config.feed_url is missing")
	}
}

func TestResolve_EmptyFeedURL(t *testing.T) {
	c := New()
	err := c.Resolve(context.Background(), connector.Config{Name: "blog", Config: map[string]string{"feed_url": "  "}}, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when config.feed_url is blank")
	}
}

func TestResolve_SetsFeedURLAndClient(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:   "leon-blog",
		Config: map[string]string{"feed_url": "https://blog.getleon.ai/rss.xml"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.feedURL != "https://blog.getleon.ai/rss.xml" {
		t.Errorf("feedURL = %q", c.feedURL)
	}
	if c.name != "leon-blog" {
		t.Errorf("name = %q", c.name)
	}
	if c.client == nil {
		t.Fatal("client not initialized")
	}
}
