package transport

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func testClient(t *testing.T, srv *httptest.Server, opts ...func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		Doer:       srv.Client(),
		MaxRetries: 3,
		BaseDelay:  time.Millisecond,
		MaxDelay:   5 * time.Millisecond,
		Sleep:      func(ctx context.Context, d time.Duration) error { return nil },
		JitterFunc: func() float64 { return 1 },
	}
	for _, o := range opts {
		o(&cfg)
	}
	return NewClient(cfg)
}

func TestClientRetriesOn5xxThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestClientRetriesPOSTWithBody(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		bodies = append(bodies, string(b))
		if len(bodies) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString("payload"))
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	if bodies[1] != "payload" {
		t.Fatalf("retried body = %q, want %q", bodies[1], "payload")
	}
}

func TestClientExhaustsRetriesOn5xx(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := c.Do(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want 4 (1 + 3 retries)", calls)
	}
}

func TestClientRetriesOn429WithFakeClock(t *testing.T) {
	var calls int
	var slept []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 2 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := testClient(t, srv, func(cfg *Config) {
		cfg.Sleep = func(ctx context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		}
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(slept) != 1 || slept[0] < 2*time.Second {
		t.Fatalf("slept = %v, want >= 2s honoring Retry-After", slept)
	}
}

func TestClientRateLimitResetHeader(t *testing.T) {
	var calls int
	var slept []time.Duration
	reset := time.Now().Add(3 * time.Second).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 2 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := testClient(t, srv, func(cfg *Config) {
		cfg.Sleep = func(ctx context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		}
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if len(slept) != 1 || slept[0] < time.Second {
		t.Fatalf("slept = %v, want to honor X-RateLimit-Reset", slept)
	}
}

func TestClientETag304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("body"))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("If-None-Match", `"v1"`)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", resp.StatusCode)
	}
}

func TestClientProxyBypass(t *testing.T) {
	fallbackCalled := false
	fn := ProxyBypassFunc([]string{"internal.example"}, func(*http.Request) (*url.URL, error) {
		fallbackCalled = true
		return url.Parse("http://proxy.example:8118")
	})

	req, _ := http.NewRequest(http.MethodGet, "http://internal.example/x", nil)
	u, err := fn(req)
	if err != nil {
		t.Fatalf("proxy func: %v", err)
	}
	if u != nil {
		t.Fatalf("expected direct (nil proxy) for no-proxy host, got %v", u)
	}
	if fallbackCalled {
		t.Fatal("fallback should not be called for no-proxy host")
	}

	reqOther, _ := http.NewRequest(http.MethodGet, "http://external.example/x", nil)
	u2, err := fn(reqOther)
	if err != nil {
		t.Fatalf("proxy func: %v", err)
	}
	if u2 == nil {
		t.Fatal("expected proxy for non-listed host")
	}
}
