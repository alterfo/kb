package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("slack", func() connector.Connector { return New() })
}

type Connector struct {
	name     string
	apiBase  string
	token    string
	channels []string
	client   *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "slack" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.apiBase = strings.TrimRight(defaultStr(cfg.Config["base_url"], "https://slack.com/api"), "/")

	c.channels = nil
	if v := strings.TrimSpace(cfg.Config["channels"]); v != "" {
		for _, ch := range strings.Split(v, ",") {
			ch = strings.TrimSpace(ch)
			if ch != "" {
				c.channels = append(c.channels, ch)
			}
		}
	}
	if len(c.channels) == 0 {
		return fmt.Errorf("slack: source %q: config.channels is required", cfg.Name)
	}

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}
	if c.token == "" {
		return fmt.Errorf("slack: source %q: secrets.token is required", cfg.Name)
	}

	if c.client == nil {
		c.client = transport.NewClient(transport.Config{})
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	st := parseCursorState(since.Value)
	fullReconcile := len(st.Since) == 0

	newSince := map[string]string{}
	for k, v := range st.Since {
		newSince[k] = v
	}

	count := 0
	for _, channel := range c.channels {
		oldest := st.Since[channel]
		maxTs := oldest
		if err := c.fetchChannel(ctx, channel, oldest, out, &count, &maxTs); err != nil {
			return since, connector.FetchInfo{}, fmt.Errorf("slack: channel %s: %w", channel, err)
		}
		if maxTs != "" {
			newSince[channel] = maxTs
		}
	}

	newState := cursorState{Since: newSince}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile}, nil
}

func (c *Connector) fetchChannel(ctx context.Context, channel, oldest string, out chan<- connector.Document, count *int, maxTs *string) error {
	q := url.Values{}
	q.Set("channel", channel)
	q.Set("limit", "200")
	if oldest != "" {
		q.Set("oldest", oldest)
	}
	req, err := c.newRequest(ctx, http.MethodGet, c.apiBase+"/conversations.history?"+q.Encode())
	if err != nil {
		return err
	}

	pager := transport.CursorFieldPager{Path: "response_metadata.next_cursor", Param: "cursor"}
	return c.client.Paginate(ctx, req, pager, func(resp *http.Response, body []byte) error {
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		var page apiHistoryResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		if !page.OK {
			return fmt.Errorf("api error: %s", page.Error)
		}
		for _, m := range page.Messages {
			d := buildDocument(c.name, channel, m)
			if err := send(ctx, out, d); err != nil {
				return err
			}
			*count++
			if compareTs(m.Ts, *maxTs) > 0 {
				*maxTs = m.Ts
			}
		}
		return nil
	})
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
	req.Header.Set("Authorization", "Bearer "+c.token)
	return req, nil
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
