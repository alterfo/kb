package wiki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

func confluenceContentJSON(id, title, spaceKey string, version int, when string) string {
	return `{"id":"` + id + `","title":"` + title + `","space":{"key":"` + spaceKey + `"},"version":{"number":` + strconv.Itoa(version) + `,"when":"` + when + `"},"body":{"storage":{"value":"<p>` + title + `</p>"}},"_links":{"webui":"/spaces/` + spaceKey + `/pages/` + id + `"}}`
}

func TestConfluence_BasicAuthHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "bot@acme.com" || pass != "api-token" {
			t.Errorf("BasicAuth = (%q,%q,%v), want bot@acme.com/api-token", user, pass, ok)
		}
		w.Write([]byte(`{"results":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "confluence", "base_url": srv.URL},
		map[string]string{"email": "CONF_EMAIL", "token": "CONF_TOKEN"},
		map[string]string{"CONF_EMAIL": "bot@acme.com", "CONF_TOKEN": "api-token"})

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestConfluence_NoAuthHeaderWhenUnset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		w.Write([]byte(`{"results":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "confluence", "base_url": srv.URL}, nil, nil)

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestConfluence_CQLLastModifiedCursorAndSpaceFilter(t *testing.T) {
	mux := http.NewServeMux()
	var lastCQL string
	mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, r *http.Request) {
		lastCQL = r.URL.Query().Get("cql")
		w.Write([]byte(`{"results":[` + confluenceContentJSON("100", "Home", "ENG", 3, "2026-02-01T00:00:00.000Z") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "confluence", "base_url": srv.URL, "space": "ENG"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(lastCQL, `space="ENG"`) {
		t.Fatalf("cql = %q, want space filter", lastCQL)
	}
	if strings.Contains(lastCQL, "lastmodified") {
		t.Fatalf("first fetch should not filter by lastmodified, cql = %q", lastCQL)
	}
	if !info1.FullReconcile {
		t.Fatalf("first fetch should be FullReconcile")
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
	if !strings.Contains(lastCQL, `lastmodified >= "2026-02-01 00:00"`) {
		t.Fatalf("cql = %q, want lastmodified cursor", lastCQL)
	}
	if info2.FullReconcile {
		t.Fatalf("second fetch should not be FullReconcile")
	}
}

func TestConfluence_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "confluence", "base_url": srv.URL}, nil, nil)

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

func TestConfluence_NextLinkPagination(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("start") == "" {
			w.Write([]byte(`{"results":[` + confluenceContentJSON("1", "Page One", "ENG", 1, "2026-01-01T00:00:00.000Z") + `],"_links":{"next":"/rest/api/content/search?cql=type%3Dpage&start=1"}}`))
			return
		}
		w.Write([]byte(`{"results":[` + confluenceContentJSON("2", "Page Two", "ENG", 1, "2026-01-02T00:00:00.000Z") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "confluence", "base_url": srv.URL}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Fatalf("search calls = %d, want 2 (next-link pagination)", calls)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(*docs))
	}
}

func TestConfluence_AncestorsAndFrontmatter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"id":"55","title":"Setup Guide","space":{"key":"ENG"},"version":{"number":4,"when":"2026-03-01T00:00:00.000Z"},"ancestors":[{"title":"Root"},{"title":"Docs"}],"body":{"storage":{"value":"<p>content</p>"}},"_links":{"webui":"/spaces/ENG/pages/55"}}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "confluence", "base_url": srv.URL}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(*docs))
	}
	d := (*docs)[0]
	if d.ID != "confluence:ENG:55" {
		t.Errorf("ID = %q, want confluence:ENG:55", d.ID)
	}
	if d.URL != srv.URL+"/spaces/ENG/pages/55" {
		t.Errorf("URL = %q", d.URL)
	}
	ancestors, ok := d.Frontmatter["ancestors"].([]string)
	if !ok || len(ancestors) != 2 || ancestors[0] != "Root" || ancestors[1] != "Docs" {
		t.Errorf("ancestors = %v", d.Frontmatter["ancestors"])
	}
}

func TestConfluence_StatusErrorPropagates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"variant": "confluence", "base_url": srv.URL}, nil, nil)

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error on 401 response")
	}
}
