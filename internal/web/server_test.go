package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHealth_Boot(t *testing.T) {
	te := newTestEnv(t, nil)
	ts := httptest.NewServer(te.server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
}

func TestSameOrigin_BlocksCrossOriginPost(t *testing.T) {
	te := newTestEnv(t, nil)
	h := te.server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/add", strings.NewReader(url.Values{"path": {"x"}, "content": {"y"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST status = %d, want 403", rr.Code)
	}
}

func TestSameOrigin_AllowsSameOriginPost(t *testing.T) {
	te := newTestEnv(t, nil)
	h := te.server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/add", strings.NewReader(url.Values{"path": {"notes/x.md"}, "content": {"y"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "http://127.0.0.1")
	req.Header.Set("Referer", "http://127.0.0.1/add")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code == http.StatusForbidden {
		t.Error("same-origin POST was blocked")
	}
}

func TestSameOrigin_BlocksNonLoopbackHost(t *testing.T) {
	te := newTestEnv(t, nil)
	h := te.server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/add", strings.NewReader(url.Values{"path": {"x"}, "content": {"y"}}.Encode()))
	req.Host = "evil.example"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Referer", "http://evil.example/add")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("non-loopback host POST status = %d, want 403", rr.Code)
	}
}

func TestSameOrigin_BlocksRefererMismatch(t *testing.T) {
	te := newTestEnv(t, nil)
	h := te.server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/add", strings.NewReader("a=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "http://evil.example/add")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("mismatched Referer POST status = %d, want 403", rr.Code)
	}
}

func TestHome_ShowsCounters(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/a.md", doc("a", "notes", "alpha one two three"))
	te.index(t, "notes/a.md")

	rr := getPage(t, te.server.Handler(), "/")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Overview", "documents", "chunks", "corpus version"} {
		if !strings.Contains(body, want) {
			t.Errorf("home page missing %q", want)
		}
	}
}
