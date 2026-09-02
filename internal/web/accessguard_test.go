package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
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
