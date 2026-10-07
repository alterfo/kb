package searxng

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultTimeout          = 10 * time.Second
	DefaultMaxResponseBytes = 1 << 20
	DefaultMaxResults       = 5

	maxTitleRunes   = 200
	maxURLRunes     = 2048
	maxSnippetRunes = 500
	maxPerDomain    = 2
)

var (
	ErrDisabled         = errors.New("searxng: web search is disabled")
	ErrResponseTooLarge = errors.New("searxng: response exceeds size limit")
)

type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	BaseURL          string
	Doer             HTTPDoer
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxResults       int
}

type Client struct {
	baseURL          string
	doer             HTTPDoer
	timeout          time.Duration
	maxResponseBytes int64
	maxResults       int
}

func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = DefaultMaxResults
	}
	if cfg.Doer == nil {
		cfg.Doer = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{
		baseURL:          strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		doer:             cfg.Doer,
		timeout:          cfg.Timeout,
		maxResponseBytes: cfg.MaxResponseBytes,
		maxResults:       cfg.MaxResults,
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != ""
}

func (c *Client) Search(ctx context.Context, query string, n int) ([]Result, error) {
	if c == nil || !c.Enabled() {
		return nil, ErrDisabled
	}
	if n <= 0 {
		n = c.maxResults
	}

	u, err := url.Parse(c.baseURL + "/search")
	if err != nil {
		return nil, fmt.Errorf("searxng: parse base url: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("categories", "general")
	u.RawQuery = q.Encode()

	reqCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("searxng: build request: %w", err)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, c.maxResponseBytes))
		return nil, fmt.Errorf("searxng: unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("searxng: read body: %w", err)
	}
	if int64(len(data)) > c.maxResponseBytes {
		return nil, ErrResponseTooLarge
	}

	var parsed rawResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("searxng: decode response: %w", err)
	}

	return c.normalizeAll(parsed.Results, n), nil
}

type rawResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
	Snippet string `json:"snippet"`
}

type rawResponse struct {
	Results []rawResult `json:"results"`
}

func (c *Client) normalizeAll(raws []rawResult, n int) []Result {
	results := make([]Result, 0, n)
	seenURL := make(map[string]bool)
	perDomain := make(map[string]int)

	for _, raw := range raws {
		if len(results) >= n {
			break
		}
		r, ok := normalizeResult(raw)
		if !ok {
			continue
		}
		key := strings.ToLower(r.URL)
		if seenURL[key] {
			continue
		}
		host := resultHost(r.URL)
		if perDomain[host] >= maxPerDomain {
			continue
		}
		seenURL[key] = true
		perDomain[host]++
		results = append(results, r)
	}
	return results
}

func normalizeResult(raw rawResult) (Result, bool) {
	rawURL := strings.TrimSpace(raw.URL)
	if !validHTTPURL(rawURL) {
		return Result{}, false
	}
	return Result{
		Title:   truncateRunes(stripHTML(raw.Title), maxTitleRunes),
		URL:     truncateRunes(rawURL, maxURLRunes),
		Snippet: truncateRunes(stripHTML(firstNonEmpty(raw.Content, raw.Snippet)), maxSnippetRunes),
	}, true
}

var htmlTagRE = regexp.MustCompile(`(?i)<[^>]*>`)

var whitespaceRE = regexp.MustCompile(`\s+`)

func stripHTML(s string) string {
	s = htmlTagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(whitespaceRE.ReplaceAllString(s, " "))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func resultHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	return strings.ToLower(u.Hostname())
}
