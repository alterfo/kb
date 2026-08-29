package web

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/markdown"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("web", func() connector.Connector { return New() })
}

type Connector struct {
	name            string
	sitemapURL      string
	pages           []string
	contentSelector string
	client          *transport.Client
	logf            func(format string, args ...any)
}

const maxPageBytes = 32 << 20

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "web" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.sitemapURL = strings.TrimSpace(cfg.Config["sitemap_url"])

	c.pages = nil
	if raw := strings.TrimSpace(cfg.Config["pages"]); raw != "" {
		for _, page := range strings.Split(raw, ",") {
			page = strings.TrimSpace(page)
			if page != "" {
				c.pages = append(c.pages, page)
			}
		}
	}
	if c.sitemapURL == "" && len(c.pages) == 0 {
		return fmt.Errorf("web: source %q: config.sitemap_url or config.pages is required", cfg.Name)
	}

	c.contentSelector = strings.ToLower(strings.TrimSpace(cfg.Config["content_selector"]))
	if c.contentSelector == "" {
		c.contentSelector = "main"
	}

	if c.client == nil {
		c.client = transport.NewClient(transport.Config{})
	}
	if c.logf == nil {
		c.logf = log.Printf
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	pages, err := c.collectPages(ctx)
	if err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("web: %w", err)
	}

	count := 0
	failed := 0
	for _, pageURL := range pages {
		doc, ok := c.fetchPage(ctx, pageURL)
		if !ok {
			failed++
			continue
		}
		if err := send(ctx, out, doc); err != nil {
			return since, connector.FetchInfo{}, err
		}
		count++
	}
	return connector.Cursor{}, connector.FetchInfo{ItemCount: count, FullReconcile: failed == 0}, nil
}

func (c *Connector) collectPages(ctx context.Context) ([]string, error) {
	if c.sitemapURL == "" {
		return append([]string(nil), c.pages...), nil
	}
	return c.collectSitemap(ctx, c.sitemapURL, 0)
}

func (c *Connector) collectSitemap(ctx context.Context, sitemapURL string, depth int) ([]string, error) {
	if depth > 10 {
		return nil, fmt.Errorf("web: sitemap nesting too deep at %s", sitemapURL)
	}
	body, err := c.get(ctx, sitemapURL)
	if err != nil {
		return nil, err
	}
	pages, children, err := parseSitemap(body, sitemapURL)
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		sub, err := c.collectSitemap(ctx, child, depth+1)
		if err != nil {
			return nil, err
		}
		pages = append(pages, sub...)
	}
	return pages, nil
}

func parseSitemap(body []byte, sitemapURL string) ([]string, []string, error) {
	base, _ := url.Parse(sitemapURL)

	var set urlset
	setErr := xml.Unmarshal(body, &set)
	if setErr == nil {
		pages := make([]string, 0, len(set.URLs))
		for _, entry := range set.URLs {
			loc := strings.TrimSpace(entry.Loc)
			if loc == "" {
				continue
			}
			if u, err := url.Parse(loc); err == nil && base != nil && !u.IsAbs() {
				loc = base.ResolveReference(u).String()
			}
			pages = append(pages, loc)
		}
		return pages, nil, nil
	}

	var index sitemapIndex
	indexErr := xml.Unmarshal(body, &index)
	if indexErr == nil {
		children := make([]string, 0, len(index.Sitemaps))
		for _, entry := range index.Sitemaps {
			loc := strings.TrimSpace(entry.Loc)
			if loc == "" {
				continue
			}
			if u, err := url.Parse(loc); err == nil && base != nil && !u.IsAbs() {
				loc = base.ResolveReference(u).String()
			}
			children = append(children, loc)
		}
		return nil, children, nil
	}

	return nil, nil, fmt.Errorf("decode sitemap: as urlset: %w; as sitemapindex: %w", setErr, indexErr)
}

func (c *Connector) fetchPage(ctx context.Context, pageURL string) (connector.Document, bool) {
	body, err := c.get(ctx, pageURL)
	if err != nil {
		c.logf("web: skipping %s: %v", pageURL, err)
		return connector.Document{}, false
	}
	root, err := parseHTML(body)
	if err != nil {
		c.logf("web: skipping %s: parse html: %v", pageURL, err)
		return connector.Document{}, false
	}

	title := extractTitle(root)
	content := extractContent(root, c.contentSelector)
	base, _ := url.Parse(pageURL)
	md := markdown.Render(content, base)
	return buildDocument(c.name, pageURL, title, md), true
}

func (c *Connector) get(ctx context.Context, u string) ([]byte, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("web: unsupported URL scheme %q", parsed.Scheme)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(ctx, req.Clone(ctx))
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if len(body) > maxPageBytes {
		return nil, fmt.Errorf("web: response exceeds %d bytes", maxPageBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &statusError{resp.StatusCode}
	}
	return body, nil
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
