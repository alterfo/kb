package transport

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

func DefaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func ParseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func parseRateLimitReset(v string) time.Duration {
	if v == "" {
		return 0
	}
	epoch, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	d := time.Until(time.Unix(epoch, 0))
	if d < 0 {
		return 0
	}
	return d
}

func retryDelay(resp *http.Response) time.Duration {
	if d := ParseRetryAfter(resp.Header.Get("Retry-After")); d > 0 {
		return d
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return parseRateLimitReset(resp.Header.Get("X-RateLimit-Reset"))
	}
	return 0
}

func (c *Client) backoffDelay(attempt int) time.Duration {
	cap := c.baseDelay << uint(attempt)
	if cap <= 0 || cap > c.maxDelay {
		cap = c.maxDelay
	}
	return time.Duration(c.jitter() * float64(cap))
}

func (c *Client) wait(ctx context.Context, attempt int, retryAfter time.Duration) bool {
	delay := c.backoffDelay(attempt)
	if retryAfter > delay {
		delay = retryAfter
	}
	return c.sleep(ctx, delay) == nil
}
