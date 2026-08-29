package transport

import (
	"context"
	"net"
	"testing"
)

func TestSOCKS5DialContextValid(t *testing.T) {
	dial, err := SOCKS5DialContext("socks5://127.0.0.1:3333")
	if err != nil {
		t.Fatalf("SOCKS5DialContext: %v", err)
	}
	if dial == nil {
		t.Fatal("expected non-nil dial context")
	}
}

func TestSOCKS5DialContextDefaultPort(t *testing.T) {
	dial, err := SOCKS5DialContext("socks5://127.0.0.1")
	if err != nil {
		t.Fatalf("SOCKS5DialContext: %v", err)
	}
	if dial == nil {
		t.Fatal("expected non-nil dial context")
	}
}

func TestSOCKS5DialContextInvalidScheme(t *testing.T) {
	if _, err := SOCKS5DialContext("http://127.0.0.1:3333"); err == nil {
		t.Fatal("expected error for non-SOCKS scheme")
	}
}

func TestSOCKS5DialContextMissingHost(t *testing.T) {
	if _, err := SOCKS5DialContext("socks5://:3333"); err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestSOCKS5DialContextEmptyURL(t *testing.T) {
	if _, err := SOCKS5DialContext(""); err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestNewProxyBypassTransportWithDialContextUsesCustomDialer(t *testing.T) {
	called := false
	tr := NewProxyBypassTransportWithDialContext(nil, func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = true
		return nil, nil
	})
	if tr.DialContext == nil {
		t.Fatal("expected custom DialContext")
	}
	if _, err := tr.DialContext(context.Background(), "tcp", "example.com:80"); err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	if !called {
		t.Fatal("custom DialContext was not used")
	}
}

func TestNewProxyBypassTransportWithNilDialContextKeepsDefault(t *testing.T) {
	tr := NewProxyBypassTransportWithDialContext(nil, nil)
	if tr == nil {
		t.Fatal("expected transport")
	}
}

func TestNewProxyBypassTransportWithDialContextDisablesHTTPProxy(t *testing.T) {
	tr := NewProxyBypassTransportWithDialContext(nil, func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, nil
	})
	if tr.Proxy != nil {
		t.Fatal("expected HTTP proxy disabled when a dial context is provided")
	}
}
