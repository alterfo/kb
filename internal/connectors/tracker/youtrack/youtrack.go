package youtrack

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
	registry.Register("youtrack", func() connector.Connector { return New() })
}

const (
	pageSize = 100
	fields   = "idReadable,summary,description,updated,project(shortName),customFields(name,value(name,login))"
)

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

func (c *Connector) Type() string { return "youtrack" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name
	c.apiBase = strings.TrimRight(strings.TrimSpace(cfg.Config["base_url"]), "/")
	if c.apiBase == "" {
		return fmt.Errorf("youtrack: source %q: config.base_url is required", cfg.Name)
	}
	c.webBase = strings.TrimRight(defaultStr(cfg.Config["web_base_url"], c.apiBase), "/")

	c.token = ""
	if envName, ok := cfg.Secrets["token"]; ok && envName != "" {
		if v, ok := env(envName); ok {
			c.token = v
		}
	}
	if c.token == "" {
		return fmt.Errorf("youtrack: source %q: secrets.token is required", cfg.Name)
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
	if err := c.fetchIssues(ctx, st.Since, out, &count, &maxUpdated); err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("youtrack: %w", err)
	}

	newState := cursorState{Since: maxUpdated}
	newCursor := connector.Cursor{Value: newState.encode()}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile}, nil
}

func (c *Connector) fetchIssues(ctx context.Context, since time.Time, out chan<- connector.Document, count *int, maxUpdated *time.Time) error {
	skip := 0
	for {
		q := url.Values{}
		q.Set("fields", fields)
		q.Set("$skip", strconv.Itoa(skip))
		q.Set("$top", strconv.Itoa(pageSize))
		if !since.IsZero() {
			q.Set("query", fmt.Sprintf("updated: {%s} .. *", since.UTC().Format("2006-01-02 15:04:05")))
		}

		req, err := c.newAPIRequest(ctx, http.MethodGet, c.apiBase+"/api/issues?"+q.Encode())
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

		var page []apiIssue
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		for _, it := range page {
			d := buildDocument(c.name, c.webBase, it)
			if err := send(ctx, out, d); err != nil {
				return err
			}
			*count++
			if u := it.updated(); u.After(*maxUpdated) {
				*maxUpdated = u
			}
		}

		if len(page) < pageSize {
			return nil
		}
		skip += pageSize
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
	req.Header.Set("Accept", "application/json")
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
