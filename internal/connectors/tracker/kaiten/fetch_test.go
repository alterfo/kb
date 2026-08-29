package kaiten

import (
	"context"
	"fmt"
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

	full := map[string]string{"base_url": srv.URL}
	for k, v := range cfg {
		full[k] = v
	}
	if secrets == nil {
		secrets = map[string]string{"token": "KAITEN_TOKEN"}
	}
	if env == nil {
		env = map[string]string{"KAITEN_TOKEN": "secret-bearer"}
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "kt", Config: full, Secrets: secrets}, fakeEnv(env)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return c
}

func cardJSON(id int, updated, column string) string {
	return fmt.Sprintf(`{"id":%d,"title":"Card %d","description":"body %d","updated":%q,"column":{"title":%q},"board":{"title":"Board"},"owner":{"full_name":"Ivan Ivanov"}}`,
		id, id, id, updated, column)
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
	mux.HandleFunc("/api/latest/cards", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-bearer" {
			t.Errorf("Authorization = %q, want Bearer secret-bearer", got)
		}
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, nil, nil, nil)
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_OffsetPagination(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/api/latest/cards", func(w http.ResponseWriter, r *http.Request) {
		calls++
		offset := r.URL.Query().Get("offset")
		if offset == "0" {
			body := "["
			for i := 0; i < pageLimit; i++ {
				if i > 0 {
					body += ","
				}
				body += cardJSON(i, "2026-01-01T00:00:00.000Z", "Todo")
			}
			body += "]"
			w.Write([]byte(body))
			return
		}
		w.Write([]byte("[" + cardJSON(999, "2026-02-01T00:00:00.000Z", "Done") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, nil, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (full page then partial)", calls)
	}
	if info.ItemCount != pageLimit+1 {
		t.Fatalf("ItemCount = %d, want %d", info.ItemCount, pageLimit+1)
	}
	if len(*docs) != pageLimit+1 {
		t.Fatalf("docs = %d, want %d", len(*docs), pageLimit+1)
	}
}

func TestFetch_IncrementalCursorAdvances(t *testing.T) {
	var lastUpdatedAfter string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest/cards", func(w http.ResponseWriter, r *http.Request) {
		lastUpdatedAfter = r.URL.Query().Get("updated_after")
		w.Write([]byte("[" + cardJSON(1, "2026-02-01T00:00:00.000Z", "Todo") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, nil, nil, nil)

	out := make(chan connector.Document)
	_, done := drain(out)
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch #1: %v", err)
	}
	if lastUpdatedAfter != "" {
		t.Fatalf("first fetch should not send updated_after, got %q", lastUpdatedAfter)
	}
	if !info1.FullReconcile {
		t.Fatal("first fetch should be FullReconcile")
	}

	out2 := make(chan connector.Document)
	_, done2 := drain(out2)
	_, info2, err := c.Fetch(context.Background(), cursor1, out2)
	<-done2
	if err != nil {
		t.Fatalf("Fetch #2: %v", err)
	}
	if lastUpdatedAfter != "2026-02-01T00:00:00.000Z" {
		t.Fatalf("second fetch updated_after = %q, want 2026-02-01T00:00:00.000Z", lastUpdatedAfter)
	}
	if info2.FullReconcile {
		t.Fatal("second fetch should not be FullReconcile")
	}
}

func TestFetch_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest/cards", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, nil, nil, nil)
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

func TestFetch_NotFoundIsFailOpen(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest/cards", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, nil, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 0 {
		t.Fatalf("expected no docs on 404, got %d", len(*docs))
	}
}
