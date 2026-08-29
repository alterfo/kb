package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/connector"
)

// TestServeOverStdioProtocol exercises the full MCP wire protocol (schema
// generation, tool listing, and a tool call) rather than calling handler
// methods directly, to catch anything that only breaks once real
// marshaling/validation is involved.
func TestServeOverInMemoryProtocol(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	writeDoc(t, te.root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "unique-protocol-token here"})
	if err := te.indexer.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	clientTransport, serverTransport := sdk.NewInMemoryTransports()

	serverErr := make(chan error, 1)
	go func() { serverErr <- te.server.Run(ctx, serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	wantTools := map[string]bool{
		"search": false, "ask": false, "get_document": false, "list_sources": false,
		"add_note": false, "add_source": false, "graph_query": false, "generate_report": false,
		"reindex": false, "status": false,
	}
	for _, tool := range tools.Tools {
		wantTools[tool.Name] = true
	}
	for name, found := range wantTools {
		if !found {
			t.Errorf("ListTools: missing tool %q", name)
		}
	}

	res, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"query": "unique-protocol-token"},
	})
	if err != nil {
		t.Fatalf("CallTool(search): %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool(search): IsError=true, content=%+v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatalf("CallTool(search): got no content")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("session.Close: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server.Run: %v", err)
	}
}

func TestGetDocument_TraversalRejectedOverProtocol(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverErr := make(chan error, 1)
	go func() { serverErr <- te.server.Run(ctx, serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      "get_document",
		Arguments: map[string]any{"path": "../../etc/passwd"},
	})
	if err != nil {
		t.Fatalf("CallTool(get_document): %v", err)
	}
	if !res.IsError {
		t.Fatalf("CallTool(get_document): IsError=false for a traversal path, want true")
	}

	session.Close()
	<-serverErr
}

func TestToolsMatchesRegisteredTools(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverErr := make(chan error, 1)
	go func() { serverErr <- te.server.Run(ctx, serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	displayed := map[string]string{}
	for _, ti := range te.server.Tools() {
		displayed[ti.Name] = ti.Description
	}
	if len(tools.Tools) != len(displayed) {
		t.Fatalf("registered %d tools, Tools() reports %d", len(tools.Tools), len(displayed))
	}
	for _, tool := range tools.Tools {
		if displayed[tool.Name] != tool.Description {
			t.Errorf("tool %q: Tools() description drifted from registered tool", tool.Name)
		}
	}

	session.Close()
	<-serverErr
}
