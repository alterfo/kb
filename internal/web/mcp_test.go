package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPInfo_ShowsEndpointAndTools(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/mcp/info")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"/mcp", "kb mcp", "search", "graph_query", "mcpServers"} {
		if !strings.Contains(body, want) {
			t.Errorf("mcp info page missing %q: %q", want, body)
		}
	}
}

func TestMCPInfo_DegradesWithoutMCP(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{Root: root})
	rr := getPage(t, srv.Handler(), "/mcp/info")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not configured") {
		t.Errorf("expected not-configured message, got %q", rr.Body.String())
	}
}

func TestMCP_HTTPEndpointHandshakes(t *testing.T) {
	te := newTestEnv(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc": "2.0", "id": 1, "method":"initialize", "params": {}}`,
	))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()
	te.server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"protocolVersion"`) {
		t.Errorf("handshake response missing protocolVersion: %q", body)
	}
	if !strings.Contains(body, `"kb"`) {
		t.Errorf("handshake response missing server name: %q", body)
	}
}

func TestMCP_RouteAbsentWithoutMCP(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{Root: root})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusOK {
		t.Errorf("expected /mcp to be unavailable without a configured MCP server, got 200")
	}
}
