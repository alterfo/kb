package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestGuardRequiresToken(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{
		Root:        root,
		PersistDir:  filepath.Join(root, ".persist"),
		AuthToken:   "secret-token",
		RequireAuth: true,
	})
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-KB-Token", "wrong")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("valid token status = %d, want non-401", rr.Code)
	}
}

func TestGuardAllowsHealthzWithoutToken(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{
		Root:        root,
		PersistDir:  filepath.Join(root, ".persist"),
		AuthToken:   "secret-token",
		RequireAuth: true,
	})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rr.Code)
	}
}

func TestGuardRateLimitsPerClient(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{Root: root, PersistDir: filepath.Join(root, ".persist"), RateLimit: 1})
	h := srv.Handler()

	first := httptest.NewRequest(http.MethodGet, "/", nil)
	first.RemoteAddr = "192.0.2.1:1234"
	firstRR := httptest.NewRecorder()
	h.ServeHTTP(firstRR, first)

	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "192.0.2.1:1234"
	secondRR := httptest.NewRecorder()
	h.ServeHTTP(secondRR, second)

	if secondRR.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", secondRR.Code)
	}
}

// TestGuardRateLimitsUnauthenticatedRequests pins the fix ordering the
// limiter check before the auth check: the actual brute-force/abuse threat
// is unauthenticated traffic, and it must be throttled too, not just traffic
// that already has a valid token.
func TestGuardRateLimitsUnauthenticatedRequests(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{
		Root:        root,
		PersistDir:  filepath.Join(root, ".persist"),
		AuthToken:   "secret-token",
		RequireAuth: true,
		RateLimit:   1,
	})
	h := srv.Handler()

	first := httptest.NewRequest(http.MethodGet, "/", nil)
	first.RemoteAddr = "192.0.2.9:1"
	firstRR := httptest.NewRecorder()
	h.ServeHTTP(firstRR, first)
	if firstRR.Code != http.StatusUnauthorized {
		t.Fatalf("first unauthenticated request status = %d, want 401", firstRR.Code)
	}

	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "192.0.2.9:1"
	secondRR := httptest.NewRecorder()
	h.ServeHTTP(secondRR, second)
	if secondRR.Code != http.StatusTooManyRequests {
		t.Fatalf("second unauthenticated request status = %d, want 429 (rate limiter must run even when auth fails)", secondRR.Code)
	}
}

func TestClientRateLimiterEvictsExpiredClients(t *testing.T) {
	current := time.Unix(0, 0)
	now := func() time.Time { return current }
	l := newClientRateLimiter(1, now)

	for i := 0; i < clientRateLimiterSweepEvery-1; i++ {
		l.allow("client-" + strconv.Itoa(i))
	}
	current = current.Add(2 * time.Minute)
	l.allow("trigger")

	l.mu.Lock()
	n := len(l.clients)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("clients map has %d entries after the periodic sweep, want 1 (only the just-added trigger key; the %d expired entries should have been evicted, not retained forever)", n, clientRateLimiterSweepEvery-1)
	}
}
