package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/connector"
)

const stdioHelperEnv = "KB_MCP_TEST_STDIO_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(stdioHelperEnv) == "1" {
		runStdioHelperServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runStdioHelperServer() {
	s := buildFakeServer(map[string]string{"file:///a.txt": "stdio content A"}, nil)
	_ = s.Run(context.Background(), &sdk.StdioTransport{})
}

func requireExec(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("unsupported OS")
	}
}

func buildFakeServer(resources map[string]string, failURIs map[string]bool) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "0.0.1"}, nil)
	for uri, content := range resources {
		content := content
		s.AddResource(&sdk.Resource{URI: uri, Name: uri, MIMEType: "text/plain"}, func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: req.Params.URI, MIMEType: "text/plain", Text: content}}}, nil
		})
	}
	for uri := range failURIs {
		if _, ok := resources[uri]; ok {
			continue
		}
		s.AddResource(&sdk.Resource{URI: uri, Name: uri}, func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			return nil, fmt.Errorf("simulated read failure for %s", req.Params.URI)
		})
	}
	s.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "Echoes input",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{}, nil
	})
	return s
}

type contentBox struct {
	mu    sync.Mutex
	files map[string]string
}

func (b *contentBox) get() map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]string, len(b.files))
	for k, v := range b.files {
		out[k] = v
	}
	return out
}

func (b *contentBox) set(k, v string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.files[k] = v
}

func captureAuth(dst *string, mu *sync.Mutex, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*dst = r.Header.Get("Authorization")
		mu.Unlock()
		h.ServeHTTP(w, r)
	})
}

func drain(out <-chan connector.Document) (*[]connector.Document, <-chan struct{}) {
	docs := &[]connector.Document{}
	done := make(chan struct{})
	go func() {
		for d := range out {
			*docs = append(*docs, d)
		}
		close(done)
	}()
	return docs, done
}

func TestFetch_HTTP_FirstRunEmitsAllWithAuthHeader(t *testing.T) {
	box := &contentBox{files: map[string]string{
		"file:///a.txt": "Alpha content",
		"file:///b.txt": "Beta content",
	}}
	var gotAuth string
	var authMu sync.Mutex

	srv := httptest.NewServer(captureAuth(&gotAuth, &authMu, sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		return buildFakeServer(box.get(), nil)
	}, nil)))
	defer srv.Close()

	c := New()
	cfg := connector.Config{
		Name:    "src",
		Config:  map[string]string{"transport": "http", "url": srv.URL},
		Secrets: map[string]string{"token": "MCP_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"MCP_TOKEN": "tok123"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor1, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !info.FullReconcile {
		t.Error("want FullReconcile=true on first run")
	}
	if info.ItemCount != 3 || len(*docs) != 3 {
		t.Fatalf("ItemCount=%d docs=%d, want 3 (2 resources + 1 tool)", info.ItemCount, len(*docs))
	}

	authMu.Lock()
	auth := gotAuth
	authMu.Unlock()
	if auth != "Bearer tok123" {
		t.Errorf("Authorization header = %q, want Bearer tok123", auth)
	}

	st := parseCursorState(cursor1.Value)
	if len(st.Hashes) != 3 {
		t.Fatalf("cursor hashes = %d, want 3 (a.txt, b.txt, tool)", len(st.Hashes))
	}

	out2 := make(chan connector.Document)
	docs2, done2 := drain(out2)
	cursor2, info2, err := c.Fetch(context.Background(), cursor1, out2)
	<-done2
	if err != nil {
		t.Fatalf("Fetch (2nd run): %v", err)
	}
	if !info2.FullReconcile {
		t.Error("want FullReconcile=true on every complete enumeration")
	}
	if info2.ItemCount != 3 || len(*docs2) != 3 {
		t.Fatalf("2nd run ItemCount=%d docs=%d, want 3 (every run re-emits the full set so the sink can prune deletions)", info2.ItemCount, len(*docs2))
	}

	box.set("file:///a.txt", "Alpha content v2")
	out3 := make(chan connector.Document)
	docs3, done3 := drain(out3)
	_, info3, err := c.Fetch(context.Background(), cursor2, out3)
	<-done3
	if err != nil {
		t.Fatalf("Fetch (3rd run): %v", err)
	}
	if info3.ItemCount != 3 || len(*docs3) != 3 {
		t.Fatalf("3rd run ItemCount=%d docs=%d, want 3 (full enumeration)", info3.ItemCount, len(*docs3))
	}
	foundA := false
	for _, d := range *docs3 {
		if d.ID == "src:file:///a.txt" {
			foundA = true
		}
	}
	if !foundA {
		t.Error("3rd run must include the changed a.txt")
	}

	// A resource removed upstream disappears from the enumeration, so the
	// sink's prune (triggered by FullReconcile) can delete its file.
	delete(box.files, "file:///b.txt")
	out4 := make(chan connector.Document)
	docs4, done4 := drain(out4)
	_, info4, err := c.Fetch(context.Background(), cursor2, out4)
	<-done4
	if err != nil {
		t.Fatalf("Fetch (4th run): %v", err)
	}
	if !info4.FullReconcile {
		t.Error("want FullReconcile=true after a resource was removed")
	}
	if info4.ItemCount != 2 || len(*docs4) != 2 {
		t.Fatalf("4th run ItemCount=%d docs=%d, want 2 (b.txt removed)", info4.ItemCount, len(*docs4))
	}
	for _, d := range *docs4 {
		if d.ID == "src:file:///b.txt" {
			t.Fatalf("removed resource still emitted: %s", d.ID)
		}
	}
}

func TestFetch_ReadErrorSuppressesFullReconcile(t *testing.T) {
	box := &contentBox{files: map[string]string{"file:///a.txt": "content"}}
	srv := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		return buildFakeServer(box.get(), map[string]bool{"file:///err.txt": true})
	}, nil))
	defer srv.Close()

	c := New()
	if err := c.Resolve(context.Background(), connector.Config{Name: "src", Config: map[string]string{"transport": "http", "url": srv.URL}}, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// The failing read is skipped fail-open (only a.txt + tool emitted),
	// but the enumeration is incomplete: no full reconcile, so the sink
	// cannot prune the temporarily unreadable resource.
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("ItemCount=%d docs=%d, want 2 (a.txt + tool; err.txt skipped)", info.ItemCount, len(*docs))
	}
	if info.FullReconcile {
		t.Error("want FullReconcile=false when a read failed (incomplete enumeration)")
	}
}

func TestFetch_HTTP_NoAuthWhenTokenAbsent(t *testing.T) {
	var gotAuth string
	var authMu sync.Mutex
	srv := httptest.NewServer(captureAuth(&gotAuth, &authMu, sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		return buildFakeServer(map[string]string{"file:///a.txt": "content"}, nil)
	}, nil)))
	defer srv.Close()

	c := New()
	cfg := connector.Config{Name: "src", Config: map[string]string{"transport": "http", "url": srv.URL}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	authMu.Lock()
	auth := gotAuth
	authMu.Unlock()
	if auth != "" {
		t.Errorf("Authorization header = %q, want empty", auth)
	}
}

func TestFetch_Stdio_Smoke(t *testing.T) {
	requireExec(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(stdioHelperEnv, "1")

	c := New()
	cfg := connector.Config{Name: "src", Config: map[string]string{"transport": "stdio", "command": exe}}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(nil)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !info.FullReconcile {
		t.Error("want FullReconcile=true on first run")
	}
	if len(*docs) != 2 {
		t.Fatalf("got %d docs, want 2 (1 resource + 1 tool)", len(*docs))
	}
}

func TestFetch_UnknownTransport(t *testing.T) {
	c := &Connector{name: "src", transportKind: "carrier-pigeon"}
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error for unknown transport")
	}
}
