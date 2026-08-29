package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("telegram", func() connector.Connector { return New() })
}

const pageLimit = 100

type Connector struct {
	name    string
	apiBase string
	token   string
	client  *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "telegram" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.apiBase = strings.TrimRight(defaultStr(cfg.Config["base_url"], "https://api.telegram.org"), "/")

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}
	if c.token == "" {
		return fmt.Errorf("telegram: source %q: secrets.token is required", cfg.Name)
	}

	if c.client == nil {
		c.client = transport.NewClient(transport.Config{})
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	st := parseCursorState(since.Value)

	offset := st.Offset
	count := 0
	// Bound a single sync even on a feed that stays full page after page
	// (production rate at or above consumption rate never yields a short
	// page); the offset still advances, so the next sync continues where
	// this one stopped.
	const maxPages = 1000
	for pages := 0; ; pages++ {
		if pages >= maxPages {
			break
		}
		req, err := c.newRequest(ctx, offset)
		if err != nil {
			return since, connector.FetchInfo{}, err
		}
		resp, err := c.do(ctx, req)
		if err != nil {
			return since, connector.FetchInfo{}, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return since, connector.FetchInfo{}, err
		}
		if resp.StatusCode != http.StatusOK {
			return since, connector.FetchInfo{}, &statusError{resp.StatusCode}
		}

		var page apiUpdatesResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return since, connector.FetchInfo{}, err
		}
		if !page.OK {
			return since, connector.FetchInfo{}, fmt.Errorf("telegram: %s", page.Description)
		}
		if len(page.Result) == 0 {
			break
		}

		for _, u := range page.Result {
			offset = u.UpdateID + 1
			msg := u.Message
			if msg == nil {
				msg = u.ChannelPost
			}
			if msg == nil {
				msg = u.EditedMessage
			}
			if msg == nil {
				msg = u.EditedChannelPost
			}
			if msg == nil {
				continue
			}
			d := buildDocument(c.name, *msg)
			if err := send(ctx, out, d); err != nil {
				return since, connector.FetchInfo{}, err
			}
			count++
		}

		if len(page.Result) < pageLimit {
			break
		}
	}

	newState := cursorState{Offset: offset}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count}, nil
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Connector) newRequest(ctx context.Context, offset int64) (*http.Request, error) {
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("limit", strconv.Itoa(pageLimit))
	u := c.apiBase + "/bot" + c.token + "/getUpdates?" + q.Encode()
	return http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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
