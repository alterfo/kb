package web

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
	if got := New().Type(); got != "web" {
		t.Fatalf("Type() = %q, want web", got)
	}
}

func TestResolve_RequiresSitemapOrPages(t *testing.T) {
	err := New().Resolve(context.Background(), connector.Config{Name: "docs"}, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when neither sitemap_url nor pages is configured")
	}
}

func TestResolve_BlankSitemapAndPages(t *testing.T) {
	cfg := connector.Config{Name: "docs", Config: map[string]string{"sitemap_url": "  ", "pages": " , "}}
	err := New().Resolve(context.Background(), cfg, fakeEnv(nil))
	if err == nil {
		t.Fatal("expected error when sitemap_url and pages are blank")
	}
}

func TestResolve_SitemapURL(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "leon-docs", Config: map[string]string{"sitemap_url": "https://docs.getleon.ai/sitemap.xml"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.sitemapURL != "https://docs.getleon.ai/sitemap.xml" {
		t.Errorf("sitemapURL = %q", c.sitemapURL)
	}
	if c.contentSelector != "main" {
		t.Errorf("contentSelector = %q, want main", c.contentSelector)
	}
	if c.client == nil {
		t.Fatal("client not initialized")
	}
}

func TestResolve_Pages(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "site", Config: map[string]string{"pages": "https://a.example/1, https://a.example/2 , https://a.example/3"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(c.pages) != 3 {
		t.Fatalf("pages = %v, want 3", c.pages)
	}
	if c.pages[1] != "https://a.example/2" {
		t.Errorf("pages[1] = %q", c.pages[1])
	}
}

func TestResolve_CustomSelector(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "site", Config: map[string]string{"pages": "https://a.example", "content_selector": "article"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.contentSelector != "article" {
		t.Errorf("contentSelector = %q, want article", c.contentSelector)
	}
}
