package blog

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("rss", func() connector.Connector { return New() })
}

type Connector struct {
	name    string
	feedURL string
	client  *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "rss" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.feedURL = strings.TrimSpace(cfg.Config["feed_url"])
	if c.feedURL == "" {
		return fmt.Errorf("rss: source %q: config.feed_url is required", cfg.Name)
	}

	if c.client == nil {
		c.client = transport.NewClient(transport.Config{})
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	body, err := c.get(ctx, c.feedURL)
	if err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("rss: %w", err)
	}

	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("rss: decode feed: %w", err)
	}

	count := 0
	for _, item := range feed.Channel.Items {
		d := buildDocument(c.name, item)
		if d.ID == "" {
			continue
		}
		if err := send(ctx, out, d); err != nil {
			return since, connector.FetchInfo{}, err
		}
		count++
	}
	return connector.Cursor{}, connector.FetchInfo{ItemCount: count, FullReconcile: true}, nil
}

func (c *Connector) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(ctx, req.Clone(ctx))
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
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
