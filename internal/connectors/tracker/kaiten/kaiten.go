package kaiten

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("kaiten", func() connector.Connector { return New() })
}

const pageLimit = 100

type Connector struct {
	name    string
	apiBase string
	webBase string
	token   string
	client  *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "kaiten" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.apiBase = strings.TrimRight(strings.TrimSpace(cfg.Config["base_url"]), "/")
	if c.apiBase == "" {
		return fmt.Errorf("kaiten: source %q: config.base_url is required", cfg.Name)
	}
	c.webBase = strings.TrimRight(defaultStr(cfg.Config["web_base_url"], c.apiBase), "/")

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}
	if c.token == "" {
		return fmt.Errorf("kaiten: source %q: secrets.token is required", cfg.Name)
	}

	if c.client == nil {
		c.client = transport.NewClient(transport.Config{})
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	st := parseCursorState(since.Value)
	fullReconcile := st.Since.IsZero()

	count := 0
	maxUpdated := st.Since
	if err := c.fetchCards(ctx, st.Since, out, &count, &maxUpdated); err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("kaiten: %w", err)
	}

	newState := cursorState{Since: maxUpdated}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile}, nil
}

func (c *Connector) fetchCards(ctx context.Context, since time.Time, out chan<- connector.Document, count *int, maxUpdated *time.Time) error {
	offset := 0
	for {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(pageLimit))
		q.Set("offset", strconv.Itoa(offset))
		if !since.IsZero() {
			q.Set("updated_after", since.UTC().Format(timeFormatLayout))
		}

		req, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+"/api/latest/cards?"+q.Encode())
		if err != nil {
			return err
		}
		resp, err := c.do(ctx, req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}

		var page []apiCard
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		for _, card := range page {
			d := buildDocument(c.name, c.webBase, card)
			if err := send(ctx, out, d); err != nil {
				return err
			}
			*count++
			if u := card.updated(); u.After(*maxUpdated) {
				*maxUpdated = u
			}
		}

		if len(page) < pageLimit {
			return nil
		}
		offset += pageLimit
	}
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Connector) newAPIRequest(ctx context.Context, method, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return req, nil
}

func (c *Connector) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	return c.client.Do(ctx, req.Clone(ctx))
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
