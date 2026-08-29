package mattermost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("mattermost", func() connector.Connector { return New() })
}

const perPage = 200

type Connector struct {
	name     string
	apiBase  string
	webBase  string
	team     string
	token    string
	channels []string
	client   *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "mattermost" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.apiBase = strings.TrimRight(strings.TrimSpace(cfg.Config["base_url"]), "/")
	if c.apiBase == "" {
		return fmt.Errorf("mattermost: source %q: config.base_url is required", cfg.Name)
	}
	c.webBase = strings.TrimRight(defaultStr(cfg.Config["web_base_url"], c.apiBase), "/")
	c.team = strings.TrimSpace(cfg.Config["team"])

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
		return fmt.Errorf("mattermost: source %q: config.channels is required", cfg.Name)
	}

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}
	if c.token == "" {
		return fmt.Errorf("mattermost: source %q: secrets.token is required", cfg.Name)
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

	newSince := map[string]int64{}
	for k, v := range st.Since {
		newSince[k] = v
	}

	count := 0
	for _, channel := range c.channels {
		sinceMs := st.Since[channel]
		maxCreate := sinceMs
		if err := c.fetchChannel(ctx, channel, sinceMs, out, &count, &maxCreate); err != nil {
			return since, connector.FetchInfo{}, fmt.Errorf("mattermost: channel %s: %w", channel, err)
		}
		if maxCreate > 0 {
			newSince[channel] = maxCreate
		}
	}

	newState := cursorState{Since: newSince}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile}, nil
}

func (c *Connector) fetchChannel(ctx context.Context, channel string, sinceMs int64, out chan<- connector.Document, count *int, maxCreate *int64) error {
	page := 0
	for {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(perPage))
		if sinceMs > 0 {
			q.Set("since", strconv.FormatInt(sinceMs, 10))
		}
		req, err := c.newRequest(ctx, http.MethodGet, c.apiBase+"/api/v4/channels/"+url.PathEscape(channel)+"/posts?"+q.Encode())
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

		var page_ apiPostsResponse
		if err := json.Unmarshal(body, &page_); err != nil {
			return err
		}

		ids := make([]string, len(page_.Order))
		copy(ids, page_.Order)
		sort.Slice(ids, func(i, j int) bool { return page_.Posts[ids[i]].CreateAt < page_.Posts[ids[j]].CreateAt })

		for _, id := range ids {
			p := page_.Posts[id]
			d := buildDocument(c.name, c.webBase, c.team, channel, p)
			if err := send(ctx, out, d); err != nil {
				return err
			}
			*count++
			if p.CreateAt > *maxCreate {
				*maxCreate = p.CreateAt
			}
		}

		if len(page_.Order) < perPage {
			return nil
		}
		page++
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

func (c *Connector) newRequest(ctx context.Context, method, u string) (*http.Request, error) {
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
