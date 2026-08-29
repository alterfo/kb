package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

func newTestConnector(t *testing.T, srv *httptest.Server, cfg map[string]string) *Connector {
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
	c.logf = func(format string, args ...any) {}
	if err := c.Resolve(context.Background(), connector.Config{Name: "leon-docs", Config: cfg}, fakeEnv(nil)); err != nil {
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

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

func sitemapBody() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>/page-a</loc></url>
  <url><loc>/page-b</loc></url>
</urlset>`
}

func TestFetch_SitemapParsesAndFetchesPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sitemapBody()))
	})
	mux.HandleFunc("/page-a", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>A</title></head><body><main><h1>A</h1><p>Alpha</p></main></body></html>`))
	})
	mux.HandleFunc("/page-b", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>B</title></head><body><main><h1>B</h1><p>Beta</p></main></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"sitemap_url": srv.URL + "/sitemap.xml"})
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(*docs))
	}
	if info.ItemCount != 2 {
		t.Fatalf("ItemCount = %d, want 2", info.ItemCount)
	}
	if !info.FullReconcile {
		t.Fatal("FullReconcile = false, want true")
	}

	first := (*docs)[0]
	if first.ID != "page-a" {
		t.Errorf("first ID = %q, want page-a", first.ID)
	}
	if first.Kind != "doc_page" {
		t.Errorf("first Kind = %q, want doc_page", first.Kind)
	}
	if first.Title != "A" {
		t.Errorf("first Title = %q, want A", first.Title)
	}
	if first.Body != "# A\n\nAlpha" {
		t.Errorf("first Body = %q", first.Body)
	}
	if first.URL != srv.URL+"/page-a" {
		t.Errorf("first URL = %q", first.URL)
	}
}

func TestFetch_PagesModeWithoutSitemap(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>A</title></head><body><h1>A</h1><p>Alpha</p></body></html>`))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>B</title></head><body><h1>B</h1><p>Beta</p></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"pages": srv.URL + "/a," + srv.URL + "/b"})
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(*docs))
	}
	if info.ItemCount != 2 {
		t.Fatalf("ItemCount = %d, want 2", info.ItemCount)
	}
}

func TestFetch_ContentExtractionBySelector(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "page.html"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"pages": srv.URL + "/"})
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
	body := (*docs)[0].Body
	if body == "" {
		t.Fatal("body is empty")
	}
	if strings.Contains(body, "Home") {
		t.Errorf("nav content leaked into body: %q", body)
	}
	if !strings.Contains(body, "Getting Started") {
		t.Errorf("main content missing from body: %q", body)
	}
}

func TestFetch_FallbackWhenNoMain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "no-main.html"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"pages": srv.URL + "/"})
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
	body := (*docs)[0].Body
	if !strings.Contains(body, "This page has no main or article element.") {
		t.Errorf("body fallback failed: %q", body)
	}
}

func TestFetch_HTTPErrorSkipsPageNotWholeFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>/ok</loc></url>
  <url><loc>/missing</loc></url>
</urlset>`))
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>OK</title></head><body><main><p>Fine</p></main></body></html>`))
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"sitemap_url": srv.URL + "/sitemap.xml"})
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1 (failed page skipped)", len(*docs))
	}
	if info.ItemCount != 1 {
		t.Fatalf("ItemCount = %d, want 1", info.ItemCount)
	}
	if info.FullReconcile {
		t.Fatal("FullReconcile = true, want false when a page fetch failed")
	}
}

func TestFetch_SitemapIndexFetchesChildSitemaps(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>/child1.xml</loc></sitemap>
  <sitemap><loc>/child2.xml</loc></sitemap>
</sitemapindex>`))
	})
	mux.HandleFunc("/child1.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>/a</loc></url></urlset>`))
	})
	mux.HandleFunc("/child2.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>/b</loc></url></urlset>`))
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>A</title></head><body><main><p>A</p></main></body></html>`))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>B</title></head><body><main><p>B</p></main></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"sitemap_url": srv.URL + "/sitemap.xml"})
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
}

func TestFetch_SitemapNotFoundIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"sitemap_url": srv.URL + "/sitemap.xml"})
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error when sitemap is not found")
	}
}

func TestFetch_InvalidSitemapXMLIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<urlset><url><loc>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"sitemap_url": srv.URL + "/sitemap.xml"})
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error on invalid sitemap XML")
	}
}
