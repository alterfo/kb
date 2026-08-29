package llm

import (
	"context"
	"time"
)

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
