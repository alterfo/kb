package weeek

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

	full := map[string]string{"base_url": srv.URL, "web_base_url": srv.URL}
	for k, v := range cfg {
		full[k] = v
	}
	if secrets == nil {
		secrets = map[string]string{"token": "WEEEK_TOKEN"}
	}
	if env == nil {
		env = map[string]string{"WEEEK_TOKEN": "secret-bearer"}
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "wk", Config: full, Secrets: secrets}, fakeEnv(env)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return c
}

func taskJSON(id int) string {
	return fmt.Sprintf(`{"id":%d,"title":"Task %d","description":"body %d","boardName":"Board","columnName":"Todo","updatedAt":"2026-01-01T00:00:00.000Z","responsibleIds":[7]}`, id, id, id)
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
	mux.HandleFunc("/public/v1/tm/tasks", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-bearer" {
			t.Errorf("Authorization = %q, want Bearer secret-bearer", got)
		}
		w.Write([]byte(`{"success":true,"tasks":[]}`))
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
	mux.HandleFunc("/public/v1/tm/tasks", func(w http.ResponseWriter, r *http.Request) {
		calls++
		offset := r.URL.Query().Get("offset")
		if offset == "0" {
			body := `{"success":true,"tasks":[`
			for i := 0; i < pageLimit; i++ {
				if i > 0 {
					body += ","
				}
				body += taskJSON(i)
			}
			body += "]}"
			w.Write([]byte(body))
			return
		}
		w.Write([]byte(`{"success":true,"tasks":[` + taskJSON(999) + `]}`))
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
	if !info.FullReconcile {
		t.Fatal("FullReconcile should always be true (no incremental cursor field)")
	}
}

func TestFetch_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/public/v1/tm/tasks", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/public/v1/tm/tasks", func(w http.ResponseWriter, r *http.Request) {
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
