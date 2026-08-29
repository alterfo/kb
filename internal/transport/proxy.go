package transport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/proxy"
)

func ProxyBypassFunc(noProxyHosts []string, fallback func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	set := make(map[string]struct{}, len(noProxyHosts))
	for _, h := range noProxyHosts {
		set[h] = struct{}{}
	}
	return func(req *http.Request) (*url.URL, error) {
		if _, ok := set[req.URL.Hostname()]; ok {
			return nil, nil
		}
		return fallback(req)
	}
}

func NewProxyBypassTransport(noProxyHosts []string) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = ProxyBypassFunc(noProxyHosts, http.ProxyFromEnvironment)
	return t
}

// NewProxyBypassTransportWithDialContext is NewProxyBypassTransport plus an
// explicit outbound DialContext (used by the Discord connector for SOCKS5).
// A non-nil dialContext disables the HTTP proxy and uses the dialer directly;
// a nil dialContext leaves the default behavior untouched.
func NewProxyBypassTransportWithDialContext(noProxyHosts []string, dialContext func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Transport {
	t := NewProxyBypassTransport(noProxyHosts)
	if dialContext != nil {
		t.DialContext = dialContext
		t.Proxy = nil
	}
	return t
}

// SOCKS5DialContext parses a socks5:// (or socks5h://) proxy URL and returns
// a context-aware DialContext for the proxy. Username/password auth is
// supported from the URL user-info when present.
func SOCKS5DialContext(rawURL string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("transport: empty SOCKS proxy URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("transport: parse SOCKS proxy URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "socks5" && scheme != "socks5h" {
		return nil, fmt.Errorf("transport: SOCKS proxy scheme %q (want socks5 or socks5h)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("transport: SOCKS proxy URL is missing a host")
	}
	port := u.Port()
	if port == "" {
		port = "1080"
	}
	address := net.JoinHostPort(u.Hostname(), port)

	var auth *proxy.Auth
	if u.User != nil {
		password, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: password}
	}

	dialer, err := proxy.SOCKS5("tcp", address, auth, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("transport: SOCKS5 dialer for %s: %w", address, err)
	}
	cd, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("transport: SOCKS5 dialer for %s does not support context", address)
	}
	return cd.DialContext, nil
}
