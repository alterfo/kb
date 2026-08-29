package youtrack

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
		secrets = map[string]string{"token": "YT_TOKEN"}
	}
	if env == nil {
		env = map[string]string{"YT_TOKEN": "secret-perm-token"}
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "yt", Config: full, Secrets: secrets}, fakeEnv(env)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return c
}

func issueJSON(id string, updatedMs int64, state string) string {
	return fmt.Sprintf(`{"idReadable":%q,"summary":"Issue %s","description":"body %s","updated":%d,"project":{"shortName":"KB"},"customFields":[{"name":"State","value":{"name":%q}},{"name":"Assignee","value":{"login":"ivanov"}}]}`,
		id, id, id, updatedMs, state)
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
	mux.HandleFunc("/api/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-perm-token" {
			t.Errorf("Authorization = %q, want Bearer secret-perm-token", got)
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

func TestFetch_SkipTopPagination(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/api/issues", func(w http.ResponseWriter, r *http.Request) {
		calls++
		skip := r.URL.Query().Get("$skip")
		if skip == "0" {
			body := "["
			for i := 0; i < pageSize; i++ {
				if i > 0 {
					body += ","
				}
				body += issueJSON(fmt.Sprintf("KB-%d", i), 1000, "Open")
			}
			body += "]"
			w.Write([]byte(body))
			return
		}
		w.Write([]byte("[" + issueJSON("KB-last", 2000, "Open") + "]"))
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
	if info.ItemCount != pageSize+1 {
		t.Fatalf("ItemCount = %d, want %d", info.ItemCount, pageSize+1)
	}
	if len(*docs) != pageSize+1 {
		t.Fatalf("docs = %d, want %d", len(*docs), pageSize+1)
	}
}

func TestFetch_IncrementalCursorAdvances(t *testing.T) {
	var lastQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/issues", func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.Query().Get("query")
		w.Write([]byte("[" + issueJSON("KB-1", 1700000000000, "Open") + "]"))
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
	if lastQuery != "" {
		t.Fatalf("first fetch should not send a query filter, got %q", lastQuery)
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
	if lastQuery == "" {
		t.Fatal("second fetch should send a query filter with the cursor timestamp")
	}
	if info2.FullReconcile {
		t.Fatal("second fetch should not be FullReconcile")
	}
}

func TestFetch_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/issues", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/api/issues", func(w http.ResponseWriter, r *http.Request) {
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
