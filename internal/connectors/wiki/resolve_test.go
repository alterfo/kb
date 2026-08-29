package wiki

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
	if got := New().Type(); got != "wiki" {
		t.Fatalf("Type() = %q, want wiki", got)
	}
}

func TestResolve_RequiresValidVariant(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "w", Config: map[string]string{"base_url": "https://wiki.example/w/api.php"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err == nil {
		t.Fatal("expected error when config.variant is missing")
	}

	c2 := New()
	cfg2 := connector.Config{Name: "w", Config: map[string]string{"variant": "bogus", "base_url": "https://wiki.example/w/api.php"}}
	if err := c2.Resolve(context.Background(), cfg2, fakeEnv(nil)); err == nil {
		t.Fatal("expected error for unknown variant")
	}
}

func TestResolve_RequiresBaseURL(t *testing.T) {
	c := New()
	cfg := connector.Config{Name: "w", Config: map[string]string{"variant": "mediawiki"}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err == nil {
		t.Fatal("expected error when config.base_url is missing")
	}
}

func TestResolve_MediaWikiDefaults(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:   "public-wiki",
		Config: map[string]string{"variant": "mediawiki", "base_url": "https://en.wikipedia.org/w/api.php"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.wikiName != "en.wikipedia.org" {
		t.Errorf("wikiName = %q, want en.wikipedia.org", c.wikiName)
	}
	if c.webBase != "https://en.wikipedia.org" {
		t.Errorf("webBase = %q, want https://en.wikipedia.org", c.webBase)
	}
	if c.namespace != "0" {
		t.Errorf("namespace = %q, want 0", c.namespace)
	}
}

func TestResolve_MediaWikiTokenFromEnv(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "private-wiki",
		Config:  map[string]string{"variant": "mediawiki", "base_url": "https://wiki.example/w/api.php"},
		Secrets: map[string]string{"token": "MW_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"MW_TOKEN": "bot-token"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.token != "bot-token" {
		t.Errorf("token = %q, want bot-token", c.token)
	}
}

func TestResolve_ConfluenceDefaultsAndSecrets(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "corp-wiki",
		Config:  map[string]string{"variant": "confluence", "base_url": "https://acme.atlassian.net/wiki/", "space": "ENG"},
		Secrets: map[string]string{"email": "CONF_EMAIL", "token": "CONF_TOKEN"},
	}
	err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{
		"CONF_EMAIL": "bot@acme.com",
		"CONF_TOKEN": "api-token",
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.apiBase != "https://acme.atlassian.net/wiki" {
		t.Errorf("apiBase = %q, want trailing slash trimmed", c.apiBase)
	}
	if c.webBase != c.apiBase {
		t.Errorf("webBase = %q, want equal to apiBase for confluence", c.webBase)
	}
	if c.space != "ENG" {
		t.Errorf("space = %q, want ENG", c.space)
	}
	if c.email != "bot@acme.com" || c.token != "api-token" {
		t.Errorf("email/token = %q/%q", c.email, c.token)
	}
}

func TestResolve_MissingSecretEnvIsFailOpen(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name:    "corp-wiki",
		Config:  map[string]string{"variant": "confluence", "base_url": "https://acme.atlassian.net/wiki"},
		Secrets: map[string]string{"email": "CONF_EMAIL", "token": "CONF_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.email != "" || c.token != "" {
		t.Errorf("email/token = %q/%q, want empty (unauthenticated)", c.email, c.token)
	}
}

func TestResolve_ExplicitWikiNameAndWebBaseOverride(t *testing.T) {
	c := New()
	cfg := connector.Config{
		Name: "custom",
		Config: map[string]string{
			"variant":      "mediawiki",
			"base_url":     "https://internal.example/api.php",
			"wiki":         "internal-kb",
			"web_base_url": "https://internal.example/docs",
		},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.wikiName != "internal-kb" {
		t.Errorf("wikiName = %q, want internal-kb", c.wikiName)
	}
	if c.webBase != "https://internal.example/docs" {
		t.Errorf("webBase = %q, want https://internal.example/docs", c.webBase)
	}
}
