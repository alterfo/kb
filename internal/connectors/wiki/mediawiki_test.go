package wiki

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

	full := map[string]string{}
	for k, v := range cfg {
		full[k] = v
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "w", Config: full, Secrets: secrets}, fakeEnv(env)); err != nil {
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

func mwParseResponse(text string) string {
	return fmt.Sprintf(`{"parse":{"wikitext":{"*":%q}}}`, text)
}

func TestMediaWiki_AuthHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer bot-secret" {
			t.Errorf("Authorization = %q, want Bearer bot-secret", got)
		}
		switch r.URL.Query().Get("action") {
		case "query":
			w.Write([]byte(`{"query":{"recentchanges":[]}}`))
		default:
			w.Write([]byte(mwParseResponse("")))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "mediawiki", "base_url": srv.URL + "/w/api.php"},
		map[string]string{"token": "MW_TOKEN"}, map[string]string{"MW_TOKEN": "bot-secret"})

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestMediaWiki_NoAuthHeaderWhenTokenUnset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		w.Write([]byte(`{"query":{"recentchanges":[]}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "mediawiki", "base_url": srv.URL + "/w/api.php"}, nil, nil)

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestMediaWiki_RCStartCursorAndAdvance(t *testing.T) {
	mux := http.NewServeMux()
	var lastRCStart string
	var lastRCDir string
	calls := 0
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "query":
			lastRCStart = r.URL.Query().Get("rcstart")
			lastRCDir = r.URL.Query().Get("rcdir")
			calls++
			if calls == 1 {
				w.Write([]byte(`{"query":{"recentchanges":[{"type":"edit","ns":0,"title":"Foo","pageid":1,"revid":10,"timestamp":"2026-02-01T00:00:00Z"}]}}`))
				return
			}
			w.Write([]byte(`{"query":{"recentchanges":[{"type":"edit","ns":0,"title":"Bar","pageid":2,"revid":12,"timestamp":"2026-02-02T00:00:00Z"}]}}`))
		default:
			w.Write([]byte(mwParseResponse("Foo content")))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "mediawiki", "base_url": srv.URL + "/w/api.php"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if lastRCStart != "" {
		t.Fatalf("first fetch should not send rcstart, got %q", lastRCStart)
	}
	if lastRCDir != "older" {
		t.Fatalf("first fetch rcdir = %q, want older (newest window)", lastRCDir)
	}
	if info1.FullReconcile {
		t.Fatalf("MediaWiki empty-cursor fetch only covers the recentchanges window, so it must never report FullReconcile")
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(*docs))
	}

	out2 := make(chan connector.Document)
	docs2, done2 := drain(out2)
	cursor2, info2, err := c.Fetch(context.Background(), cursor1, out2)
	<-done2
	if err != nil {
		t.Fatalf("Fetch #2: %v", err)
	}
	if lastRCStart != "2026-02-01T00:00:00Z" {
		t.Fatalf("second fetch rcstart = %q, want 2026-02-01T00:00:00Z", lastRCStart)
	}
	if lastRCDir != "newer" {
		t.Fatalf("second fetch rcdir = %q, want newer (forward from cursor)", lastRCDir)
	}
	if info2.FullReconcile {
		t.Fatalf("second fetch (non-empty cursor) should not be FullReconcile")
	}
	if len(*docs2) != 1 || (*docs2)[0].Title != "Bar" {
		t.Fatalf("second fetch docs = %+v, want only the change newer than the cursor", *docs2)
	}
	if cursor2.Value == cursor1.Value {
		t.Fatalf("cursor did not advance: %q", cursor2.Value)
	}
}

func TestMediaWiki_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "mediawiki", "base_url": srv.URL + "/w/api.php"}, nil, nil)

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

func TestMediaWiki_RCContinuePagination(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "query":
			calls++
			if r.URL.Query().Get("rccontinue") == "" {
				w.Write([]byte(`{"continue":{"rccontinue":"20260201000000|2"},"query":{"recentchanges":[{"type":"edit","ns":0,"title":"Foo","pageid":1,"revid":10,"timestamp":"2026-02-01T00:00:00Z"}]}}`))
				return
			}
			w.Write([]byte(`{"query":{"recentchanges":[{"type":"new","ns":0,"title":"Bar","pageid":2,"revid":11,"timestamp":"2026-02-02T00:00:00Z"}]}}`))
		default:
			w.Write([]byte(mwParseResponse("content")))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "mediawiki", "base_url": srv.URL + "/w/api.php"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Fatalf("recentchanges calls = %d, want 2 (rccontinue pagination)", calls)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(*docs))
	}
}

func TestMediaWiki_NonEditChangesSkipped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "query":
			w.Write([]byte(`{"query":{"recentchanges":[{"type":"log","ns":0,"title":"Foo","pageid":1,"revid":10,"timestamp":"2026-02-01T00:00:00Z"}]}}`))
		default:
			w.Write([]byte(mwParseResponse("content")))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "mediawiki", "base_url": srv.URL + "/w/api.php"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 0 {
		t.Fatalf("docs = %d, want 0 (log entries skipped)", len(*docs))
	}
}

func TestMediaWiki_ContentFetchFailureIsSkippedFailOpen(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "query":
			w.Write([]byte(`{"query":{"recentchanges":[{"type":"edit","ns":0,"title":"Foo","pageid":1,"revid":10,"timestamp":"2026-02-01T00:00:00Z"},{"type":"edit","ns":0,"title":"Bar","pageid":2,"revid":11,"timestamp":"2026-02-02T00:00:00Z"}]}}`))
		default:
			if r.URL.Query().Get("pageid") == "1" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(mwParseResponse("Bar content")))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "mediawiki", "base_url": srv.URL + "/w/api.php"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch should fail-open on content fetch error, got: %v", err)
	}
	if len(*docs) != 1 || (*docs)[0].Title != "Bar" {
		t.Fatalf("docs = %+v, want only Bar", *docs)
	}
}

func TestMediaWiki_DuplicatePageIDFetchedOnce(t *testing.T) {
	mux := http.NewServeMux()
	contentCalls := 0
	mux.HandleFunc("/w/api.php", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "query":
			w.Write([]byte(`{"query":{"recentchanges":[{"type":"edit","ns":0,"title":"Foo","pageid":1,"revid":10,"timestamp":"2026-02-01T00:00:00Z"},{"type":"edit","ns":0,"title":"Foo","pageid":1,"revid":9,"timestamp":"2026-01-31T00:00:00Z"}]}}`))
		default:
			contentCalls++
			w.Write([]byte(mwParseResponse("Foo content")))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "mediawiki", "base_url": srv.URL + "/w/api.php"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if contentCalls != 1 {
		t.Fatalf("content fetch calls = %d, want 1 (dedup by pageid)", contentCalls)
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(*docs))
	}
}
