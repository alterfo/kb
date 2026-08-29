package transport

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	Doer           HTTPDoer
	NoProxyHosts   []string
	MaxRetries     int
	BaseDelay      time.Duration
	MaxDelay       time.Duration
	RequestTimeout time.Duration
	Sleep          func(ctx context.Context, d time.Duration) error
	JitterFunc     func() float64
}

type Client struct {
	doer       HTTPDoer
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	reqTimeout time.Duration
	sleep      func(ctx context.Context, d time.Duration) error
	jitter     func() float64
}

// Doer returns the underlying HTTPDoer configured for this client.
func (c *Client) Doer() HTTPDoer {
	if c == nil {
		return nil
	}
	return c.doer
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
		cfg.Sleep = DefaultSleep
	}
	if cfg.JitterFunc == nil {
		cfg.JitterFunc = rand.Float64
	}
	if cfg.Doer == nil {
		cfg.Doer = &http.Client{Transport: NewProxyBypassTransport(cfg.NoProxyHosts)}
	}
	return &Client{
		doer:       cfg.Doer,
		maxRetries: cfg.MaxRetries,
		baseDelay:  cfg.BaseDelay,
		maxDelay:   cfg.MaxDelay,
		reqTimeout: cfg.RequestTimeout,
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

func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	ctx, cancel := c.withTimeout(ctx)

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		attemptReq := req.Clone(ctx)
		if attemptReq.Body != nil && attemptReq.GetBody != nil {
			body, err := attemptReq.GetBody()
			if err != nil {
				cancel()
				return nil, fmt.Errorf("transport: prepare request body: %w", err)
			}
			attemptReq.Body = body
		}

		resp, err := c.doer.Do(attemptReq)
		if err != nil {
			lastErr = err
			if attempt == c.maxRetries || !c.wait(ctx, attempt, 0) {
				break
			}
			continue
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			retryAfter := retryDelay(resp)
			drainAndClose(resp)
			lastErr = &StatusError{StatusCode: resp.StatusCode, Status: resp.Status}
			if attempt == c.maxRetries || !c.wait(ctx, attempt, retryAfter) {
				cancel()
				return nil, lastErr
			}
			continue
		case resp.StatusCode >= 500:
			retryAfter := retryDelay(resp)
			drainAndClose(resp)
			lastErr = &StatusError{StatusCode: resp.StatusCode, Status: resp.Status}
			if attempt == c.maxRetries || !c.wait(ctx, attempt, retryAfter) {
				cancel()
				return nil, lastErr
			}
			continue
		default:
			// cancel must outlive Do(): it's deferred to resp.Body.Close(), not here,
			// otherwise the caller's later body read races an in-flight cancellation.
			resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}
	}
	cancel()
	if lastErr == nil {
		lastErr = ctx.Err()
	}
	return nil, lastErr
}

func drainAndClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

type StatusError struct {
	StatusCode int
	Status     string
}

func (e *StatusError) Error() string { return "transport: " + e.Status }
