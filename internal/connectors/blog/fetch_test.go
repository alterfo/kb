package blog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	full := map[string]string{}
	for k, v := range cfg {
		full[k] = v
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "leon-blog", Config: full}, fakeEnv(nil)); err != nil {
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

func fixtureFeed(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "feed.rss"))
	if err != nil {
		t.Fatalf("reading feed fixture: %v", err)
	}
	return data
}

func TestFetch_ParsesFeedItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixtureFeed(t))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"feed_url": srv.URL + "/feed.xml"})
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
	if first.ID != "post-42" {
		t.Errorf("first ID = %q, want post-42", first.ID)
	}
	if first.Kind != "blog_post" {
		t.Errorf("first Kind = %q, want blog_post", first.Kind)
	}
	if first.Title != "Leon 2.0 is here" {
		t.Errorf("first Title = %q", first.Title)
	}
	if first.URL != "https://blog.getleon.ai/leon-2-0" {
		t.Errorf("first URL = %q", first.URL)
	}
	if want := "# Leon 2.0\n\nFull **post** body."; first.Body != want {
		t.Errorf("first Body = %q, want content:encoded markdown", first.Body)
	}
	wantUpdated := time.Date(2026, 8, 22, 10, 30, 0, 0, time.UTC)
	if !first.UpdatedAt.Equal(wantUpdated) {
		t.Errorf("first UpdatedAt = %v, want %v", first.UpdatedAt, wantUpdated)
	}
	if got := first.Frontmatter["guid"]; got != "post-42" {
		t.Errorf("first guid = %v, want post-42", got)
	}
	if got := first.Frontmatter["published"]; got != "Sat, 22 Aug 2026 10:30:00 +0000" {
		t.Errorf("first published = %v", got)
	}

	second := (*docs)[1]
	if second.ID != "https://blog.getleon.ai/weekly-update" {
		t.Errorf("second ID = %q, want link fallback", second.ID)
	}
	if second.Body != "Plain text body." {
		t.Errorf("second Body = %q, want description fallback", second.Body)
	}
	if _, ok := second.Frontmatter["guid"]; ok {
		t.Errorf("second guid should be absent, got %v", second.Frontmatter["guid"])
	}
	wantSecond := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	if !second.UpdatedAt.Equal(wantSecond) {
		t.Errorf("second UpdatedAt = %v, want %v", second.UpdatedAt, wantSecond)
	}
}

func TestFetch_InvalidXMLIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss><channel><item><title>broken</title>`))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"feed_url": srv.URL + "/feed.xml"})
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error on invalid XML")
	}
}

func TestFetch_EmptyFeedIsZeroDocs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss version="2.0"><channel><title>Empty</title></channel></rss>`))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"feed_url": srv.URL + "/feed.xml"})
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 0 {
		t.Fatalf("docs = %d, want 0", len(*docs))
	}
	if info.ItemCount != 0 {
		t.Fatalf("ItemCount = %d, want 0", info.ItemCount)
	}
}

func TestFetch_NotFoundIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"feed_url": srv.URL + "/feed.xml"})
	out := make(chan connector.Document)
	_, done := drain(out)
	cursor, _, err := c.Fetch(context.Background(), connector.Cursor{Value: "prev"}, out)
	<-done
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if cursor.Value != "prev" {
		t.Fatalf("cursor = %q, want unchanged 'prev'", cursor.Value)
	}
}

func TestFetch_ServerErrorIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"feed_url": srv.URL + "/feed.xml"})
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error on 500")
	}
}
