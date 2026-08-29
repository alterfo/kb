package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/transport"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	BaseURL           string
	APIKey            string
	NoProxyHosts      []string
	Doer              HTTPDoer
	MaxRetries        int
	BaseDelay         time.Duration
	MaxDelay          time.Duration
	RequestTimeout    time.Duration
	MaxTokens         int
	DefaultEmbedModel string
	Sleep             func(ctx context.Context, d time.Duration) error
	JitterFunc        func() float64
}

type Client struct {
	baseURL    string
	apiKey     string
	doer       HTTPDoer
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	reqTimeout time.Duration
	maxTokens  int
	embedModel string
	sleep      func(ctx context.Context, d time.Duration) error
	jitter     func() float64
}

func NewClient(cfg Config) *Client {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 200 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 5 * time.Second
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 60 * time.Second
	}
	if cfg.Sleep == nil {
		cfg.Sleep = transport.DefaultSleep
	}
	if cfg.JitterFunc == nil {
		cfg.JitterFunc = rand.Float64
	}
	if cfg.Doer == nil {
		cfg.Doer = &http.Client{Transport: transport.NewProxyBypassTransport(cfg.NoProxyHosts)}
	}
	return &Client{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     cfg.APIKey,
		doer:       cfg.Doer,
		maxRetries: cfg.MaxRetries,
		baseDelay:  cfg.BaseDelay,
		maxDelay:   cfg.MaxDelay,
		reqTimeout: cfg.RequestTimeout,
		maxTokens:  cfg.MaxTokens,
		embedModel: cfg.DefaultEmbedModel,
		sleep:      cfg.Sleep,
		jitter:     cfg.JitterFunc,
	}
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.reqTimeout)
}

func (c *Client) postJSON(ctx context.Context, path string, payload any) (*http.Response, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doWithRetry(ctx, http.MethodPost, path, bodyBytes, "application/json")
}

func (c *Client) doWithRetry(ctx context.Context, method, path string, body []byte, contentType string) (*http.Response, error) {
	url := c.baseURL + path
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.doer.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			if attempt == c.maxRetries || !c.wait(ctx, attempt, 0) {
				break
			}
			continue
		}
		if resp.StatusCode >= 500 {
			retryAfter := transport.ParseRetryAfter(resp.Header.Get("Retry-After"))
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("server error: %s", resp.Status)
			if attempt == c.maxRetries || !c.wait(ctx, attempt, retryAfter) {
				break
			}
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = ctx.Err()
	}
	return nil, lastErr
}
