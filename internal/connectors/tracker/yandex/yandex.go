package yandex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("yandex-tracker", func() connector.Connector { return New() })
}

const perPage = 100

type Connector struct {
	name    string
	apiBase string
	webBase string
	orgID   string
	token   string
	queues  []string
	client  *transport.Client
}

func New() *Connector {
	return &Connector{}
}

func (c *Connector) Type() string { return "yandex-tracker" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.apiBase = strings.TrimRight(defaultStr(cfg.Config["base_url"], "https://api.tracker.yandex.net"), "/")
	c.webBase = strings.TrimRight(defaultStr(cfg.Config["web_base_url"], "https://tracker.yandex.ru"), "/")
	c.orgID = strings.TrimSpace(cfg.Config["org_id"])
	if c.orgID == "" {
		return fmt.Errorf("yandex-tracker: source %q: config.org_id is required", cfg.Name)
	}

	c.queues = nil
	for _, q := range strings.Split(cfg.Config["queues"], ",") {
		q = strings.TrimSpace(q)
		if q != "" {
			c.queues = append(c.queues, q)
		}
	}
	if len(c.queues) == 0 {
		return fmt.Errorf("yandex-tracker: source %q: config.queues is required", cfg.Name)
	}

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}
	if c.token == "" {
		return fmt.Errorf("yandex-tracker: source %q: secrets.token is required", cfg.Name)
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

	newSince := map[string]time.Time{}
	for k, v := range st.Since {
		newSince[k] = v
	}

	count := 0
	for _, queue := range c.queues {
		queueSince := st.Since[queue]
		maxUpdated := queueSince
		if err := c.fetchQueue(ctx, queue, queueSince, out, &count, &maxUpdated); err != nil {
			return since, connector.FetchInfo{}, fmt.Errorf("yandex-tracker: queue %s: %w", queue, err)
		}
		if maxUpdated.After(queueSince) {
			newSince[queue] = maxUpdated
		}
	}

	newState := cursorState{Since: newSince}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile}, nil
}

func (c *Connector) fetchQueue(ctx context.Context, queue string, since time.Time, out chan<- connector.Document, count *int, maxUpdated *time.Time) error {
	page := 1
	for {
		body := buildSearchBody(queue, since)
		u := fmt.Sprintf("%s/v2/issues/_search?perPage=%d&page=%d", c.apiBase, perPage, page)
		req, err := c.newAPIRequest(ctx, http.MethodPost, u, body)
		if err != nil {
			return err
		}
		resp, err := c.do(ctx, req)
		if err != nil {
			return err
		}
		respBody, err := io.ReadAll(resp.Body)
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

		var items []apiIssue
		if err := json.Unmarshal(respBody, &items); err != nil {
			return err
		}
		for _, it := range items {
			d := buildDocument(c.name, c.webBase, queue, it)
			if err := send(ctx, out, d); err != nil {
				return err
			}
			*count++
			if u := it.updated(); u.After(*maxUpdated) {
				*maxUpdated = u
			}
		}

		totalPages := 1
		if v := resp.Header.Get("X-Total-Pages"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				totalPages = n
			}
		}
		if len(items) == 0 || page >= totalPages {
			return nil
		}
		page++
	}
}

func buildSearchBody(queue string, since time.Time) []byte {
	filter := map[string]any{"queue": queue}
	if !since.IsZero() {
		filter["updated"] = map[string]string{"from": since.UTC().Format(timeLayout)}
	}
	b, _ := json.Marshal(map[string]any{"filter": filter})
	return b
}

func send(ctx context.Context, out chan<- connector.Document, d connector.Document) error {
	select {
	case out <- d:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Connector) newAPIRequest(ctx context.Context, method, u string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "OAuth "+c.token)
	req.Header.Set("X-Org-ID", c.orgID)
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
