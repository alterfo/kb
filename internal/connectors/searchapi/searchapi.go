package searchapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/transport"
)

func init() {
	registry.Register("searchapi", func() connector.Connector { return New() })
}

type Connector struct {
	name string

	searchURL  string
	query      string
	queryParam string
	itemsPath  string
	kind       string
	visibility string

	sinceParam  string
	sinceLayout string

	fields fieldMap

	pagerCfg pagerConfig

	auth connector.AuthSpec

	client *transport.Client
	now    func() time.Time
}

func New() *Connector {
	return &Connector{now: time.Now}
}

func (c *Connector) Type() string { return "searchapi" }

func (c *Connector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	c.name = cfg.Name

	c.searchURL = strings.TrimSpace(cfg.Config["search_url"])
	if c.searchURL == "" {
		return fmt.Errorf("searchapi: source %q: config.search_url is required", cfg.Name)
	}
	c.query = cfg.Config["query"]
	c.queryParam = defaultStr(cfg.Config["query_param"], "q")
	c.itemsPath = cfg.Config["items_path"]
	c.kind = defaultStr(cfg.Config["kind"], "result")
	c.visibility = cfg.Config["visibility"]

	c.sinceParam = cfg.Config["since_param"]
	c.sinceLayout = defaultStr(cfg.Config["since_layout"], time.RFC3339)

	c.fields = fieldMap{
		ID:        defaultStr(cfg.Config["field_id"], "id"),
		Title:     defaultStr(cfg.Config["field_title"], "title"),
		URL:       defaultStr(cfg.Config["field_url"], "url"),
		UpdatedAt: defaultStr(cfg.Config["field_updated_at"], "updated_at"),
		Body:      defaultStr(cfg.Config["field_body"], "body"),
		Extra:     map[string]string{},
	}
	for k, v := range cfg.Config {
		if strings.HasPrefix(k, "fm_") && v != "" {
			c.fields.Extra[strings.TrimPrefix(k, "fm_")] = v
		}
	}

	step, err := time.ParseDuration(defaultStr(cfg.Config["pager_step"], "24h"))
	if err != nil {
		return fmt.Errorf("searchapi: source %q: invalid pager_step: %w", cfg.Name, err)
	}
	if step <= 0 {
		return fmt.Errorf("searchapi: source %q: pager_step must be positive", cfg.Name)
	}
	pageSize, err := strconv.Atoi(defaultStr(cfg.Config["pager_page_size"], "25"))
	if err != nil || pageSize <= 0 {
		pageSize = 25
	}
	var until time.Time
	if raw := strings.TrimSpace(cfg.Config["pager_until"]); raw != "" {
		until, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return fmt.Errorf("searchapi: source %q: invalid pager_until: %w", cfg.Name, err)
		}
	}
	c.pagerCfg = pagerConfig{
		Kind:      cfg.Config["pager"],
		Header:    cfg.Config["pager_header"],
		Param:     defaultStr(cfg.Config["pager_param"], "offset"),
		Path:      cfg.Config["pager_path"],
		PageSize:  pageSize,
		CountPath: cfg.Config["pager_count_path"],
		Layout:    defaultStr(cfg.Config["pager_layout"], c.sinceLayout),
		Step:      step,
		Until:     until,
		NowFn:     func() time.Time { return c.now() },
	}

	kind, err := parseAuthKind(cfg.Config["auth_kind"])
	if err != nil {
		return fmt.Errorf("searchapi: source %q: %w", cfg.Name, err)
	}
	c.auth = connector.AuthSpec{Kind: kind, Header: defaultStr(cfg.Config["auth_header"], "X-Api-Key")}

	resolveSecret := func(key string) string {
		if envName, ok := cfg.Secrets[key]; ok && envName != "" {
			if v, ok := env(envName); ok {
				return v
			}
		}
		return ""
	}

	switch c.auth.Kind {
	case connector.AuthBearer, connector.AuthAPIKey:
		c.auth.Token = resolveSecret("token")
		if c.auth.Token == "" {
			return fmt.Errorf("searchapi: source %q: secrets.token is required for auth_kind %q", cfg.Name, c.auth.Kind)
		}
	case connector.AuthBasic:
		c.auth.Username = resolveSecret("username")
		c.auth.Password = resolveSecret("password")
		if c.auth.Username == "" || c.auth.Password == "" {
			return fmt.Errorf("searchapi: source %q: secrets.username and secrets.password are required for auth_kind basic", cfg.Name)
		}
	}

	if c.client == nil {
		c.client = transport.NewClient(transport.Config{})
	}
	return nil
}

func parseAuthKind(v string) (connector.AuthKind, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "none":
		return connector.AuthNone, nil
	case "bearer":
		return connector.AuthBearer, nil
	case "basic":
		return connector.AuthBasic, nil
	case "apikey", "api_key":
		return connector.AuthAPIKey, nil
	default:
		return connector.AuthNone, fmt.Errorf("unknown auth_kind %q", v)
	}
}

func (c *Connector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)

	st := parseCursorState(since.Value)
	fullReconcile := c.sinceParam == "" || st.Since.IsZero()
	maxUpdated := st.Since

	req, err := c.newRequest(ctx, st.Since)
	if err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("searchapi: %w", err)
	}

	count := 0
	pager := buildPager(c.pagerCfg)
	err = c.client.Paginate(ctx, req, pager, func(resp *http.Response, body []byte) error {
		if resp.StatusCode != http.StatusOK {
			return &statusError{resp.StatusCode}
		}
		items := c.items(body)
		for _, item := range items {
			doc, updated, ok := buildDocument(c.name, c.kind, c.visibility, c.fields, c.sinceLayout, item)
			if !ok {
				continue
			}
			if err := send(ctx, out, doc); err != nil {
				return err
			}
			count++
			if updated.After(maxUpdated) {
				maxUpdated = updated
			}
		}
		return nil
	})
	if err != nil {
		return since, connector.FetchInfo{}, fmt.Errorf("searchapi: %w", err)
	}

	newCursor := connector.Cursor{}
	if c.sinceParam != "" {
		newCursor = connector.Cursor{Value: cursorState{Since: maxUpdated}.encode()}
	}
	return newCursor, connector.FetchInfo{ItemCount: count, FullReconcile: fullReconcile}, nil
}

func (c *Connector) items(body []byte) []gjson.Result {
	root := gjson.ParseBytes(body)
	if c.itemsPath != "" {
		root = root.Get(c.itemsPath)
	}
	if !root.IsArray() {
		return nil
	}
	return root.Array()
}

func (c *Connector) newRequest(ctx context.Context, since time.Time) (*http.Request, error) {
	u, err := url.Parse(c.searchURL)
	if err != nil {
		return nil, fmt.Errorf("invalid search_url: %w", err)
	}
	q := u.Query()
	if c.query != "" {
		q.Set(c.queryParam, c.query)
	}
	if c.sinceParam != "" && !since.IsZero() {
		q.Set(c.sinceParam, overlapSince(since, c.sinceLayout).Format(c.sinceLayout))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	return req, nil
}

func overlapSince(since time.Time, layout string) time.Time {
	step := time.Second
	switch {
	case strings.Contains(layout, "15:04:05"):
		step = time.Second
	case strings.Contains(layout, "15:04"):
		step = time.Minute
	case strings.Contains(layout, "15"):
		step = time.Hour
	case strings.Contains(layout, "02"):
		step = 24 * time.Hour
	}
	return since.Add(-step)
}

func (c *Connector) applyAuth(req *http.Request) {
	switch c.auth.Kind {
	case connector.AuthBearer:
		req.Header.Set("Authorization", "Bearer "+c.auth.Token)
	case connector.AuthBasic:
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	case connector.AuthAPIKey:
		header := defaultStr(c.auth.Header, "X-Api-Key")
		req.Header.Set(header, c.auth.Token)
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

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
