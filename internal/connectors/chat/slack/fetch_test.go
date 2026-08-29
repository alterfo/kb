package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

func newTestConnector(t *testing.T, srv *httptest.Server, channels string) *Connector {
	t.Helper()
	c := New()
	c.client = transport.NewClient(transport.Config{
		Doer:       srv.Client(),
		MaxRetries: 2,
		BaseDelay:  time.Millisecond,
		MaxDelay:   5 * time.Millisecond,
		Sleep:      func(ctx context.Context, d time.Duration) error { return nil },
		JitterFunc: func() float64 { return 1 },
	})
	cfg := connector.Config{
		Name:    "sl",
		Config:  map[string]string{"base_url": srv.URL, "channels": channels},
		Secrets: map[string]string{"token": "SLACK_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"SLACK_TOKEN": "xoxb-secret"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return c
}

func drain(out chan connector.Document) (*[]connector.Document, <-chan struct{}) {
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

func TestFetch_AuthHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer xoxb-secret" {
			t.Errorf("Authorization = %q, want Bearer xoxb-secret", got)
		}
		w.Write([]byte(`{"ok":true,"messages":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1")
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_FullReconcileOnEmptyCursorThenIncrement(t *testing.T) {
	mux := http.NewServeMux()
	var lastOldest string
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		lastOldest = r.URL.Query().Get("oldest")
		w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U1","text":"hello","ts":"1700000000.000100"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1")
	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if lastOldest != "" {
		t.Fatalf("first fetch should not send oldest=, got %q", lastOldest)
	}
	if !info1.FullReconcile {
		t.Fatal("first fetch (empty cursor) should be FullReconcile")
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(*docs))
	}

	out2 := make(chan connector.Document)
	_, done2 := drain(out2)
	_, info2, err := c.Fetch(context.Background(), cursor1, out2)
	<-done2
	if err != nil {
		t.Fatalf("Fetch #2: %v", err)
	}
	if lastOldest != "1700000000.000100" {
		t.Fatalf("second fetch oldest = %q, want 1700000000.000100", lastOldest)
	}
	if info2.FullReconcile {
		t.Fatal("second fetch (non-empty cursor) should not be FullReconcile")
	}
}

func TestFetch_NextCursorPagination(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "page2" {
			w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U2","text":"second","ts":"1700000002.000100"}]}`))
			return
		}
		w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U1","text":"first","ts":"1700000001.000100"}],"response_metadata":{"next_cursor":"page2"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2 (both pages)", len(*docs))
	}
}

func TestFetch_MultipleChannelsFetched(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		ch := r.URL.Query().Get("channel")
		w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U1","text":"hi from ` + ch + `","ts":"1700000003.000100"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1,C2")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2 (one per channel)", len(*docs))
	}
}

func TestFetch_ThreadedFrontmatter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"messages":[
			{"type":"message","user":"U1","text":"parent","ts":"1700000010.000100","thread_ts":"1700000010.000100"},
			{"type":"message","user":"U2","text":"reply","ts":"1700000011.000100","thread_ts":"1700000010.000100"}
		]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(*docs))
	}
	if (*docs)[0].Frontmatter["thread"] != "1700000010.000100" {
		t.Errorf("thread root = %v, want own ts so the chain glues into one chunk", (*docs)[0].Frontmatter["thread"])
	}
	if (*docs)[1].Frontmatter["thread"] != "1700000010.000100" {
		t.Errorf("reply thread = %v, want parent ts", (*docs)[1].Frontmatter["thread"])
	}
}

func TestFetch_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1")
	out := make(chan connector.Document)
	_, done := drain(out)
	cursorIn := connector.Cursor{Value: "should-not-change"}
	cursorOut, _, err := c.Fetch(context.Background(), cursorIn, out)
	<-done
	if err == nil {
		t.Fatal("expected error from 500 responses")
	}
	if cursorOut.Value != cursorIn.Value {
		t.Fatalf("cursor rolled forward on error: got %q, want unchanged %q", cursorOut.Value, cursorIn.Value)
	}
}

func TestFetch_APIErrorOKFalse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1")
	out := make(chan connector.Document)
	_, done := drain(out)
	cursorIn := connector.Cursor{Value: "unchanged"}
	cursorOut, _, err := c.Fetch(context.Background(), cursorIn, out)
	<-done
	if err == nil {
		t.Fatal("expected error when ok:false")
	}
	if cursorOut.Value != cursorIn.Value {
		t.Fatal("cursor changed on ok:false error")
	}
}
