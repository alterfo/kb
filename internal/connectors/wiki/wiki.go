package wiki

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("wiki", func() connector.Connector { return New() })
}

type Connector struct {
	name      string
	variant   string
	apiBase   string
	webBase   string
	wikiName  string
	namespace string
	space     string
	token     string
	email     string
	client    *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "wiki" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.variant = strings.ToLower(strings.TrimSpace(cfg.Config["variant"]))
	if c.variant != "mediawiki" && c.variant != "confluence" {
		return fmt.Errorf("wiki: source %q: config.variant must be %q or %q", cfg.Name, "mediawiki", "confluence")
	}

	c.apiBase = strings.TrimRight(strings.TrimSpace(cfg.Config["base_url"]), "/")
	if c.apiBase == "" {
		return fmt.Errorf("wiki: source %q: config.base_url is required", cfg.Name)
	}

	c.namespace = defaultStr(cfg.Config["namespace"], "0")
	c.space = strings.TrimSpace(cfg.Config["space"])

	c.wikiName = strings.TrimSpace(cfg.Config["wiki"])
	if c.wikiName == "" {
		if u, err := url.Parse(c.apiBase); err == nil && u.Host != "" {
			c.wikiName = u.Host
		} else {
			c.wikiName = cfg.Name
		}
	}

	if c.variant == "confluence" {
		c.webBase = c.apiBase
	} else {
		c.webBase = strings.TrimRight(defaultStr(cfg.Config["web_base_url"], deriveMediaWikiWebBase(c.apiBase)), "/")
	}

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}
	c.email = ""
	if envName, ok := cfg.Secrets["email"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.email = v
		}
	}

	if c.client == nil {
		c.client = transport.NewClient(transport.Config{})
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	st := parseCursorState(since.Value)
	// Confluence drops the lastmodified filter on an empty cursor and
	// enumerates the whole space, so an empty cursor is a genuine full
	// reconcile. MediaWiki's empty-cursor fetch only covers the server's
	// recentchanges retention window, so it can never claim a full
	// enumeration: pruning on it would delete every page last edited
	// before the window.
	fullReconcile := st.Since.IsZero() && c.variant == "confluence"

	count := 0
	maxUpdated := st.Since

	var err error
	if c.variant == "confluence" {
		err = c.fetchConfluence(ctx, st.Since, out, &count, &maxUpdated)
	} else {
		err = c.fetchMediaWiki(ctx, st.Since, out, &count, &maxUpdated)
	}
	if err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("wiki: %w", err)
	}

	newState := cursorState{Since: maxUpdated}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile}, nil
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Connector) newRequest(ctx context.Context, method, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)
	return req, nil
}

func (c *Connector) setAuth(req *http.Request) {
	if c.variant == "confluence" {
		if c.email != "" && c.token != "" {
			req.SetBasicAuth(c.email, c.token)
		}
		return
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func (c *Connector) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	return c.client.Do(ctx, req.Clone(ctx))
}

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("wiki: unexpected status %d", e.code)
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func deriveMediaWikiWebBase(apiBase string) string {
	if strings.HasSuffix(apiBase, "/w/api.php") {
		return strings.TrimSuffix(apiBase, "/w/api.php")
	}
	if strings.HasSuffix(apiBase, "/api.php") {
		return strings.TrimSuffix(apiBase, "/api.php")
	}
	return apiBase
}
