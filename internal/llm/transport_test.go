package llm

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/alterfo/kb/internal/transport"
)

func TestBuildProxyFunc_HostInNoProxyList_ReturnsDirect(t *testing.T) {
	fallbackCalled := false
	fallback := func(*http.Request) (*url.URL, error) {
		fallbackCalled = true
		return url.Parse("http://proxy.example:8118")
	}
	proxyFn := transport.ProxyBypassFunc([]string{"127.0.0.1"}, fallback)

	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:11434/v1/embeddings", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	got, err := proxyFn(req)
	if err != nil {
		t.Fatalf("proxyFn returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil (DIRECT) proxy URL, got %v", got)
	}
	if fallbackCalled {
		t.Fatal("fallback should not be called for no-proxy host")
	}
}

func TestBuildProxyFunc_HostNotInNoProxyList_UsesFallback(t *testing.T) {
	fallbackCalled := false
	wantURL, _ := url.Parse("http://proxy.example:8118")
	fallback := func(*http.Request) (*url.URL, error) {
		fallbackCalled = true
		return wantURL, nil
	}
	proxyFn := transport.ProxyBypassFunc([]string{"127.0.0.1"}, fallback)

	req, err := http.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	got, err := proxyFn(req)
	if err != nil {
		t.Fatalf("proxyFn returned error: %v", err)
	}
	if !fallbackCalled {
		t.Fatal("expected fallback to be called for non-listed host")
	}
	if got == nil || got.String() != wantURL.String() {
		t.Fatalf("expected fallback proxy URL %v, got %v", wantURL, got)
	}
}

func TestBuildProxyFunc_EmptyNoProxyList_AlwaysUsesFallback(t *testing.T) {
	fallbackCalled := false
	fallback := func(*http.Request) (*url.URL, error) {
		fallbackCalled = true
		return nil, nil
	}
	proxyFn := transport.ProxyBypassFunc(nil, fallback)

	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:11434/v1/embeddings", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := proxyFn(req); err != nil {
		t.Fatalf("proxyFn returned error: %v", err)
	}
	if !fallbackCalled {
		t.Fatal("expected fallback to be called when no-proxy list is empty")
	}
}

func TestNewProxyBypassTransport_SetsProxyFunc(t *testing.T) {
	tr := transport.NewProxyBypassTransport([]string{"127.0.0.1"})
	if tr.Proxy == nil {
		t.Fatal("expected Proxy func to be set")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:11434/v1/embeddings", nil)
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy returned error: %v", err)
	}
	if u != nil {
		t.Fatalf("expected DIRECT for no-proxy host, got %v", u)
	}
}
