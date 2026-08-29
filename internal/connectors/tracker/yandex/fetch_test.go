package yandex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

func newTestConnector(t *testing.T, srv *httptest.Server, cfg map[string]string, secrets map[string]string, env map[string]string) *Connector {
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

	full := map[string]string{"base_url": srv.URL, "web_base_url": srv.URL, "org_id": "123"}
	for k, v := range cfg {
		full[k] = v
	}
	if secrets == nil {
		secrets = map[string]string{"token": "YT_TOKEN"}
	}
	if env == nil {
		env = map[string]string{"YT_TOKEN": "secret-oauth"}
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "yt", Config: full, Secrets: secrets}, fakeEnv(env)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return c
}

func issueJSON(key, updated, status string) string {
	return fmt.Sprintf(`{"key":%q,"summary":"Issue %s","description":"body %s","status":{"display":%q},"assignee":{"display":"Ivan Ivanov"},"updatedAt":%q}`,
		key, key, key, status, updated)
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

func TestFetch_AuthAndOrgHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/issues/_search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "OAuth secret-oauth" {
			t.Errorf("Authorization = %q, want OAuth secret-oauth", got)
		}
		if got := r.Header.Get("X-Org-ID"); got != "123" {
			t.Errorf("X-Org-ID = %q, want 123", got)
		}
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"queues": "KB"}, nil, nil)
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_FilterBodySentPerQueue(t *testing.T) {
	seen := map[string]bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/issues/_search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			Filter struct {
				Queue string `json:"queue"`
			} `json:"filter"`
		}
		json.Unmarshal(body, &parsed)
		seen[parsed.Filter.Queue] = true
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"queues": "KB, OPS"}, nil, nil)
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !seen["KB"] || !seen["OPS"] {
		t.Fatalf("expected both queues queried, got %v", seen)
	}
}

func TestFetch_PaginationXTotalPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/issues/_search", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("X-Total-Pages", "2")
		if page == "2" {
			w.Write([]byte("[" + issueJSON("KB-2", "2026-02-01T00:00:00.000+0000", "Open") + "]"))
			return
		}
		w.Write([]byte("[" + issueJSON("KB-1", "2026-01-01T00:00:00.000+0000", "Open") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"queues": "KB"}, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2 (two pages)", len(*docs))
	}
	if info.ItemCount != 2 {
		t.Fatalf("ItemCount = %d, want 2", info.ItemCount)
	}
}

func TestFetch_IncrementalCursorAdvances(t *testing.T) {
	var lastFrom string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/issues/_search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			Filter struct {
				Updated *struct {
					From string `json:"from"`
				} `json:"updated"`
			} `json:"filter"`
		}
		json.Unmarshal(body, &parsed)
		if parsed.Filter.Updated != nil {
			lastFrom = parsed.Filter.Updated.From
		} else {
			lastFrom = ""
		}
		w.Write([]byte("[" + issueJSON("KB-1", "2026-02-01T00:00:00.000+0000", "Open") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"queues": "KB"}, nil, nil)

	out := make(chan connector.Document)
	_, done := drain(out)
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch #1: %v", err)
	}
	if lastFrom != "" {
		t.Fatalf("first fetch should not send updated.from, got %q", lastFrom)
	}
	if !info1.FullReconcile {
		t.Fatal("first fetch (empty cursor) should be FullReconcile")
	}

	out2 := make(chan connector.Document)
	_, done2 := drain(out2)
	_, info2, err := c.Fetch(context.Background(), cursor1, out2)
	<-done2
	if err != nil {
		t.Fatalf("Fetch #2: %v", err)
	}
	if lastFrom != "2026-02-01T00:00:00.000+0000" {
		t.Fatalf("second fetch updated.from = %q, want 2026-02-01T00:00:00.000+0000", lastFrom)
	}
	if info2.FullReconcile {
		t.Fatal("second fetch (non-empty cursor) should not be FullReconcile")
	}
}

func TestFetch_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/issues/_search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"queues": "KB"}, nil, nil)
	out := make(chan connector.Document)
	_, done := drain(out)
	cursor, _, err := c.Fetch(context.Background(), connector.Cursor{Value: "prev"}, out)
	<-done
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if cursor.Value != "prev" {
		t.Fatalf("cursor = %q, want unchanged 'prev'", cursor.Value)
	}
}

func TestFetch_UnknownQueueIsFailOpen(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/issues/_search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"queues": "MISSING"}, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 0 || info.ItemCount != 0 {
		t.Fatalf("expected no docs for 404 queue, got %d", len(*docs))
	}
}
