package searchapi

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

	full := map[string]string{"search_url": srv.URL + "/search"}
	for k, v := range cfg {
		full[k] = v
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "sa", Config: full, Secrets: secrets}, fakeEnv(env)); err != nil {
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

func itemJSON(id int, updated string) string {
	return fmt.Sprintf(`{"id":"%d","title":"Item %d","url":"https://example.com/%d","updated_at":%q,"body":"body %d"}`, id, id, id, updated, id)
}

func TestFetch_SinglePageNoPager(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[" + itemJSON(1, "2026-01-01T00:00:00Z") + "]"))
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
	if info.ItemCount != 1 || len(*docs) != 1 {
		t.Fatalf("expected 1 doc, got info=%+v docs=%d", info, len(*docs))
	}
	if (*docs)[0].ID != "1" || (*docs)[0].Title != "Item 1" {
		t.Errorf("doc = %+v", (*docs)[0])
	}
}

func TestFetch_ItemsPathAndQueryParam(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "invoices" {
			t.Errorf("q param = %q, want invoices", got)
		}
		w.Write([]byte(`{"results":[` + itemJSON(1, "2026-01-01T00:00:00Z") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"query": "invoices", "items_path": "results"}, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(*docs))
	}
}

func TestFetch_AuthApplied(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want Bearer secret", got)
		}
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"auth_kind": "bearer"},
		map[string]string{"token": "TOK"}, map[string]string{"TOK": "secret"})
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_AuthBasicAndAPIKeyApplied(t *testing.T) {
	cases := []struct {
		name     string
		authKind string
		secrets  map[string]string
		env      map[string]string
		check    func(r *http.Request) error
	}{
		{
			name:     "basic",
			authKind: "basic",
			secrets:  map[string]string{"username": "USR", "password": "PWD"},
			env:      map[string]string{"USR": "alice", "PWD": "s3cret"},
			check: func(r *http.Request) error {
				u, p, ok := r.BasicAuth()
				if !ok || u != "alice" || p != "s3cret" {
					return fmt.Errorf("BasicAuth = (%q,%q,%v), want (alice,s3cret,true)", u, p, ok)
				}
				return nil
			},
		},
		{
			name:     "apikey",
			authKind: "apikey",
			secrets:  map[string]string{"token": "TOK"},
			env:      map[string]string{"TOK": "k-123"},
			check: func(r *http.Request) error {
				if got := r.Header.Get("X-Api-Key"); got != "k-123" {
					return fmt.Errorf("X-Api-Key = %q, want k-123", got)
				}
				return nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
				if err := tc.check(r); err != nil {
					t.Errorf("%v", err)
				}
				w.Write([]byte("[]"))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			c := newTestConnector(t, srv, map[string]string{"auth_kind": tc.authKind}, tc.secrets, tc.env)
			out := make(chan connector.Document)
			_, done := drain(out)
			_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
			<-done
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
		})
	}
}

func TestFetch_LinkHeaderPager(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Link", "<http://"+r.Host+"/search?page=2>; rel=\"next\"")
			w.Write([]byte("[" + itemJSON(1, "2026-01-01T00:00:00Z") + "]"))
			return
		}
		w.Write([]byte("[" + itemJSON(2, "2026-01-02T00:00:00Z") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"pager": "link_header"}, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("expected 2 docs across pages, got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_NextPageHeaderPager(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("X-Next-Page", "2")
			w.Write([]byte("[" + itemJSON(1, "2026-01-01T00:00:00Z") + "]"))
			return
		}
		w.Write([]byte("[" + itemJSON(2, "2026-01-02T00:00:00Z") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{
		"pager":        "next_page_header",
		"pager_header": "X-Next-Page",
		"pager_param":  "page",
	}, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("expected 2 docs, got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_CursorFieldPager(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"next_cursor":"abc","items":[` + itemJSON(1, "2026-01-01T00:00:00Z") + `]}`))
			return
		}
		if got := r.URL.Query().Get("cursor"); got != "abc" {
			t.Errorf("cursor param = %q, want abc", got)
		}
		w.Write([]byte(`{"next_cursor":"","items":[` + itemJSON(2, "2026-01-02T00:00:00Z") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{
		"pager":       "cursor_field",
		"pager_path":  "next_cursor",
		"pager_param": "cursor",
		"items_path":  "items",
	}, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("expected 2 docs, got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_NextLinkPager(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"next":"/search?page=2","items":[` + itemJSON(1, "2026-01-01T00:00:00Z") + `]}`))
			return
		}
		w.Write([]byte(`{"next":"","items":[` + itemJSON(2, "2026-01-02T00:00:00Z") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{
		"pager":      "next_link",
		"pager_path": "next",
		"items_path": "items",
	}, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("expected 2 docs, got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_OffsetPager(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		if offset == "" || offset == "0" {
			w.Write([]byte(`{"total":2,"items":[` + itemJSON(1, "2026-01-01T00:00:00Z") + `]}`))
			return
		}
		w.Write([]byte(`{"total":2,"items":[` + itemJSON(2, "2026-01-02T00:00:00Z") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{
		"pager":            "offset",
		"pager_param":      "offset",
		"pager_page_size":  "1",
		"pager_count_path": "total",
		"items_path":       "items",
	}, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 2 || len(*docs) != 2 {
		t.Fatalf("expected 2 docs, got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_TimeWindowPager(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte("[" + itemJSON(calls, "2026-01-0"+fmt.Sprint(calls)+"T00:00:00Z") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	fixedNow := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	c := newTestConnector(t, srv, map[string]string{
		"pager":        "time_window",
		"pager_param":  "since",
		"pager_layout": time.RFC3339,
		"pager_step":   "24h",
		"since_param":  "since",
		"since_layout": time.RFC3339,
	}, nil, nil)
	c.now = func() time.Time { return fixedNow }

	since := connector.Cursor{Value: cursorState{Since: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}.encode()}
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), since, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.ItemCount != 4 || len(*docs) != 4 {
		t.Fatalf("expected 4 docs across time windows (initial window overlaps the cursor by 1s), got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_IncrementalCursorRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("updated_since"); got == "" {
			w.Write([]byte("[" + itemJSON(1, "2026-01-01T00:00:00Z") + "]"))
			return
		}
		w.Write([]byte("[" + itemJSON(2, "2026-01-05T00:00:00Z") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"since_param": "updated_since"}, nil, nil)

	out1 := make(chan connector.Document)
	docs1, done1 := drain(out1)
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out1)
	<-done1
	if err != nil {
		t.Fatalf("Fetch #1: %v", err)
	}
	if !info1.FullReconcile {
		t.Error("expected FullReconcile on first fetch with empty cursor")
	}
	if len(*docs1) != 1 {
		t.Fatalf("expected 1 doc on first fetch, got %d", len(*docs1))
	}
	if cursor1.Value == "" {
		t.Fatal("expected non-empty cursor after first fetch")
	}

	out2 := make(chan connector.Document)
	docs2, done2 := drain(out2)
	_, info2, err := c.Fetch(context.Background(), cursor1, out2)
	<-done2
	if err != nil {
		t.Fatalf("Fetch #2: %v", err)
	}
	if info2.FullReconcile {
		t.Error("expected non-full reconcile on second fetch with populated cursor")
	}
	if len(*docs2) != 1 {
		t.Fatalf("expected 1 doc on second fetch, got %d", len(*docs2))
	}
}

func TestFetch_NoSinceParamAlwaysFullReconcile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, nil, nil, nil)
	out := make(chan connector.Document)
	_, done := drain(out)
	newCursor, info, err := c.Fetch(context.Background(), connector.Cursor{Value: "ignored"}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !info.FullReconcile {
		t.Error("expected FullReconcile always true when since_param unset")
	}
	if newCursor.Value != "" {
		t.Errorf("newCursor.Value = %q, want empty when since_param unset", newCursor.Value)
	}
}

func TestFetch_ErrorKeepsCursorUnchanged(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, nil, nil, nil)
	prev := connector.Cursor{Value: "prev-value"}
	out := make(chan connector.Document)
	_, done := drain(out)
	newCursor, _, err := c.Fetch(context.Background(), prev, out)
	<-done
	if err == nil {
		t.Fatal("expected error on persistent 500")
	}
	if newCursor.Value != "prev-value" {
		t.Errorf("cursor rolled back to %q, want unchanged prev-value", newCursor.Value)
	}
}

func TestFetch_FailOpen_SkipsItemsMissingID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"title":"no id"},` + itemJSON(1, "2026-01-01T00:00:00Z") + `]`))
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
	if info.ItemCount != 1 || len(*docs) != 1 {
		t.Fatalf("expected 1 doc after skipping id-less item, got info=%+v docs=%d", info, len(*docs))
	}
}

func TestFetch_NonOKStatusReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, nil, nil, nil)
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error surfaced on 404 (connector does not special-case any status as fail-open)")
	}
}
