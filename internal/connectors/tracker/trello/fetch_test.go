package trello

import (
	"context"
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

	full := map[string]string{"api_base": srv.URL, "public_base": srv.URL}
	for k, v := range cfg {
		full[k] = v
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "leon-trello", Config: full, Secrets: secrets}, fakeEnv(env)); err != nil {
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

func TestFetch_PublicBoardMapsListsAndSkipsClosed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/b/abc.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"lists":[{"id":"l1","name":"Roadmap"}],
			"cards":[
				{"id":"c1","name":"Open card","desc":"Body text","due":"2026-08-22T00:00:00.000Z","closed":false,"shortUrl":"https://trello.com/c/c1","idList":"l1","labels":[{"id":"lab1","name":"Feature"},{"id":"lab2","name":""}]},
				{"id":"c2","name":"Closed card","desc":"Archive","due":null,"closed":true,"shortUrl":"https://trello.com/c/c2","idList":"l1","labels":[]}
			]
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"board_id": "abc"}, nil, nil)
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1 (closed card skipped)", len(*docs))
	}
	if info.ItemCount != 1 {
		t.Fatalf("ItemCount = %d, want 1", info.ItemCount)
	}
	if !info.FullReconcile {
		t.Fatal("FullReconcile = false, want true")
	}

	d := (*docs)[0]
	if d.ID != "abc-c1" {
		t.Errorf("ID = %q", d.ID)
	}
	if d.Kind != "trello_card" {
		t.Errorf("Kind = %q", d.Kind)
	}
	if got := d.Frontmatter["list"]; got != "Roadmap" {
		t.Errorf("list = %v, want Roadmap", got)
	}
	if got := d.Frontmatter["labels"]; got != "Feature" {
		t.Errorf("labels = %v, want Feature (blank label skipped)", got)
	}
	if got := d.Frontmatter["due"]; got != "2026-08-22T00:00:00.000Z" {
		t.Errorf("due = %v", got)
	}
}

func TestFetch_APIUsesCardsAndLists(t *testing.T) {
	mux := http.NewServeMux()
	var cardsFields string
	mux.HandleFunc("/1/boards/abc/lists", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("key"); got != "api-key" {
			t.Errorf("lists key = %q", got)
		}
		w.Write([]byte(`[{"id":"l1","name":"In Progress"}]`))
	})
	mux.HandleFunc("/1/boards/abc/cards", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("token"); got != "api-token" {
			t.Errorf("cards token = %q", got)
		}
		cardsFields = r.URL.Query().Get("fields")
		w.Write([]byte(`[{"id":"c1","name":"API card","desc":"API body","due":"2026-08-01T00:00:00.000Z","closed":false,"shortUrl":"https://trello.com/c/c1","idList":"l1","labels":[{"id":"lab1","name":"Bug"}]}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	secrets := map[string]string{"key": "TRELLO_KEY", "token": "TRELLO_TOKEN"}
	env := map[string]string{"TRELLO_KEY": "api-key", "TRELLO_TOKEN": "api-token"}
	c := newTestConnector(t, srv, map[string]string{"board_id": "abc"}, secrets, env)
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
	if cardsFields != cardFields {
		t.Errorf("cards fields = %q, want %q", cardsFields, cardFields)
	}
	if got := (*docs)[0].Frontmatter["list"]; got != "In Progress" {
		t.Errorf("list = %v, want In Progress", got)
	}
}

func TestFetch_NotFoundIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/b/abc.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"board_id": "abc"}, nil, nil)
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
	mux := http.NewServeMux()
	mux.HandleFunc("/b/abc.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"board_id": "abc"}, nil, nil)
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error on 500")
	}
}
