package mcp

import (
	"context"
	"net/http/httptest"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/connector"
)

// TestServeOverStreamableHTTPProtocol is the HTTP-transport counterpart to
// TestServeOverInMemoryProtocol in protocol_test.go. Two things were never
// exercised end-to-end before this test: the Streamable HTTP transport
// mounted by HTTPHandler (see cmd/kb/serve.go and internal/web/server.go,
// which mount it at /mcp for remote MCP clients) driving a full tool call,
// and the session semantics that only appear over real HTTP (session-id
// propagation across requests, SSE framing of the tool result) rather than
// an in-process pipe. internal/web/mcp_test.go's TestMCP_HTTPEndpointHandshakes
// only sends one raw "initialize" POST and stops there.
func TestServeOverStreamableHTTPProtocol(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	writeDoc(t, te.root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "unique-http-protocol-token here"})
	if err := te.indexer.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	ts := httptest.NewServer(te.server.HTTPHandler())
	defer ts.Close()

	client := sdk.NewClient(&sdk.Implementation{Name: "test-http-client", Version: "0.0.1"}, nil)
	transport := &sdk.StreamableClientTransport{Endpoint: ts.URL}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools over HTTP: %v", err)
	}
	if len(tools.Tools) != len(te.server.Tools()) {
		t.Fatalf("ListTools over HTTP: got %d tools, want %d", len(tools.Tools), len(te.server.Tools()))
	}

	res, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"query": "unique-http-protocol-token"},
	})
	if err != nil {
		t.Fatalf("CallTool(search) over HTTP: %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool(search) over HTTP: IsError=true, content=%+v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("CallTool(search) over HTTP: got no content")
	}

	statusRes, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(status) over HTTP: %v", err)
	}
	if statusRes.IsError {
		t.Fatalf("CallTool(status) over HTTP: IsError=true, content=%+v", statusRes.Content)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("session.Close: %v", err)
	}
}
