package llm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/transport"
)

type fakeDoer struct {
	responses []fakeResponse
	calls     int
}

type fakeResponse struct {
	status int
	header http.Header
	err    error
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	if f.calls >= len(f.responses) {
		panic("fakeDoer: no more responses configured")
	}
	r := f.responses[f.calls]
	f.calls++
	if r.err != nil {
		return nil, r.err
	}
	h := r.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{
		StatusCode: r.status,
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
	}, nil
}

func recordingSleep(records *[]time.Duration) func(context.Context, time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		*records = append(*records, d)
		return ctx.Err()
	}
}

func noWaitSleep(records *[]time.Duration) func(context.Context, time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		*records = append(*records, d)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
}

func TestDoWithRetry_SucceedsAfterServerErrors(t *testing.T) {
	doer := &fakeDoer{responses: []fakeResponse{
		{status: 500},
		{status: 502},
		{status: 200},
	}}
	var sleeps []time.Duration
	c := NewClient(Config{
		BaseURL:    "http://llm.local",
		Doer:       doer,
		MaxRetries: 3,
		BaseDelay:  time.Millisecond,
		MaxDelay:   10 * time.Millisecond,
		Sleep:      noWaitSleep(&sleeps),
		JitterFunc: func() float64 { return 1 },
	})

	resp, err := c.doWithRetry(context.Background(), http.MethodPost, "/x", nil, "")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if doer.calls != 3 {
		t.Fatalf("expected 3 calls, got %d", doer.calls)
	}
	if len(sleeps) != 2 {
		t.Fatalf("expected 2 backoff waits, got %d", len(sleeps))
	}
}

func TestDoWithRetry_ExhaustsRetriesReturnsError(t *testing.T) {
	doer := &fakeDoer{responses: []fakeResponse{
		{status: 500}, {status: 500}, {status: 500}, {status: 500},
	}}
	var sleeps []time.Duration
	c := NewClient(Config{
		BaseURL:    "http://llm.local",
		Doer:       doer,
		MaxRetries: 3,
		BaseDelay:  time.Millisecond,
		MaxDelay:   10 * time.Millisecond,
		Sleep:      noWaitSleep(&sleeps),
		JitterFunc: func() float64 { return 1 },
	})

	_, err := c.doWithRetry(context.Background(), http.MethodPost, "/x", nil, "")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if doer.calls != 4 {
		t.Fatalf("expected 4 calls (1 + 3 retries), got %d", doer.calls)
	}
}

func TestDoWithRetry_NetworkErrorRetries(t *testing.T) {
	doer := &fakeDoer{responses: []fakeResponse{
		{err: errors.New("dial tcp: connection refused")},
		{status: 200},
	}}
	var sleeps []time.Duration
	c := NewClient(Config{
		BaseURL:    "http://llm.local",
		Doer:       doer,
		MaxRetries: 3,
		BaseDelay:  time.Millisecond,
		MaxDelay:   10 * time.Millisecond,
		Sleep:      noWaitSleep(&sleeps),
		JitterFunc: func() float64 { return 1 },
	})

	resp, err := c.doWithRetry(context.Background(), http.MethodPost, "/x", nil, "")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	defer resp.Body.Close()
	if doer.calls != 2 {
		t.Fatalf("expected 2 calls, got %d", doer.calls)
	}
}

func TestDoWithRetry_HonorsRetryAfterHeader(t *testing.T) {
	doer := &fakeDoer{responses: []fakeResponse{
		{status: 503, header: http.Header{"Retry-After": []string{"2"}}},
		{status: 200},
	}}
	var sleeps []time.Duration
	c := NewClient(Config{
		BaseURL:    "http://llm.local",
		Doer:       doer,
		MaxRetries: 3,
		BaseDelay:  time.Millisecond,
		MaxDelay:   10 * time.Millisecond,
		Sleep:      noWaitSleep(&sleeps),
		JitterFunc: func() float64 { return 1 },
	})

	resp, err := c.doWithRetry(context.Background(), http.MethodPost, "/x", nil, "")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	defer resp.Body.Close()
	if len(sleeps) != 1 {
		t.Fatalf("expected 1 recorded sleep, got %d", len(sleeps))
	}
	if sleeps[0] != 2*time.Second {
		t.Fatalf("expected sleep to honor Retry-After (2s), got %v", sleeps[0])
	}
}

func TestDoWithRetry_ContextCancelledStopsRetrying(t *testing.T) {
	doer := &fakeDoer{responses: []fakeResponse{
		{status: 500}, {status: 500}, {status: 500}, {status: 500},
	}}
	var sleeps []time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewClient(Config{
		BaseURL:    "http://llm.local",
		Doer:       doer,
		MaxRetries: 3,
		BaseDelay:  time.Millisecond,
		MaxDelay:   10 * time.Millisecond,
		Sleep:      recordingSleep(&sleeps),
		JitterFunc: func() float64 { return 1 },
	})

	_, err := c.doWithRetry(ctx, http.MethodPost, "/x", nil, "")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if doer.calls != 1 {
		t.Fatalf("expected exactly 1 call before bailing on cancelled context, got %d", doer.calls)
	}
}

func TestBackoffDelay_CapsAtMaxDelay(t *testing.T) {
	c := NewClient(Config{
		BaseURL:    "http://llm.local",
		Doer:       &fakeDoer{},
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   150 * time.Millisecond,
		JitterFunc: func() float64 { return 1 },
	})
	d := c.backoffDelay(5)
	if d != 150*time.Millisecond {
		t.Fatalf("expected delay capped at maxDelay 150ms, got %v", d)
	}
}

func TestParseRetryAfter_Seconds(t *testing.T) {
	if got := transport.ParseRetryAfter("5"); got != 5*time.Second {
		t.Fatalf("expected 5s, got %v", got)
	}
}

func TestParseRetryAfter_Empty(t *testing.T) {
	if got := transport.ParseRetryAfter(""); got != 0 {
		t.Fatalf("expected 0, got %v", got)
	}
}

func TestParseRetryAfter_Invalid(t *testing.T) {
	if got := transport.ParseRetryAfter("not-a-value"); got != 0 {
		t.Fatalf("expected 0 for invalid value, got %v", got)
	}
}
