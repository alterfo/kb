package mattermost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

func newTestConnector(t *testing.T, srv *httptest.Server, channels, team string) *Connector {
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
	cfg := connector.Config{
		Name:    "mm",
		Config:  map[string]string{"base_url": srv.URL, "channels": channels, "team": team},
		Secrets: map[string]string{"token": "MM_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"MM_TOKEN": "mm-secret"})); err != nil {
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

func postsResponse(posts ...apiPost) []byte {
	resp := apiPostsResponse{Posts: map[string]apiPost{}}
	for _, p := range posts {
		resp.Order = append(resp.Order, p.ID)
		resp.Posts[p.ID] = p
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestFetch_AuthHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/C1/posts", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer mm-secret" {
			t.Errorf("Authorization = %q, want Bearer mm-secret", got)
		}
		w.Write(postsResponse())
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1", "")
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_FullReconcileOnEmptyCursorThenIncrement(t *testing.T) {
	mux := http.NewServeMux()
	var lastSince string
	mux.HandleFunc("/api/v4/channels/C1/posts", func(w http.ResponseWriter, r *http.Request) {
		lastSince = r.URL.Query().Get("since")
		w.Write(postsResponse(apiPost{ID: "p1", ChannelID: "C1", UserID: "U1", CreateAt: 1700000000000, Message: "hello"}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1", "")
	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if lastSince != "" {
		t.Fatalf("first fetch should not send since=, got %q", lastSince)
	}
	if !info1.FullReconcile {
		t.Fatal("first fetch (empty cursor) should be FullReconcile")
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
	if lastSince != "1700000000000" {
		t.Fatalf("second fetch since = %q, want 1700000000000", lastSince)
	}
	if info2.FullReconcile {
		t.Fatal("second fetch (non-empty cursor) should not be FullReconcile")
	}
}

func TestFetch_PagePagination(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/api/v4/channels/C1/posts", func(w http.ResponseWriter, r *http.Request) {
		calls++
		page := r.URL.Query().Get("page")
		if page == "0" {
			posts := make([]apiPost, perPage)
			for i := 0; i < perPage; i++ {
				posts[i] = apiPost{ID: idOf(i), ChannelID: "C1", UserID: "U1", CreateAt: int64(1700000000000 + i), Message: "m"}
			}
			w.Write(postsResponse(posts...))
			return
		}
		w.Write(postsResponse(apiPost{ID: "last", ChannelID: "C1", UserID: "U1", CreateAt: 1700000000000 + perPage, Message: "last one"}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1", "")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(*docs) != perPage+1 {
		t.Fatalf("docs = %d, want %d", len(*docs), perPage+1)
	}
}

func idOf(i int) string {
	return "p" + string(rune('a'+i%26)) + string(rune('0'+(i/26)%10))
}

func TestFetch_MultipleChannelsFetched(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/C1/posts", func(w http.ResponseWriter, r *http.Request) {
		w.Write(postsResponse(apiPost{ID: "p1", ChannelID: "C1", UserID: "U1", CreateAt: 1700000000000, Message: "c1"}))
	})
	mux.HandleFunc("/api/v4/channels/C2/posts", func(w http.ResponseWriter, r *http.Request) {
		w.Write(postsResponse(apiPost{ID: "p2", ChannelID: "C2", UserID: "U2", CreateAt: 1700000000000, Message: "c2"}))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1,C2", "")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2 (one per channel)", len(*docs))
	}
}

func TestFetch_ThreadedFrontmatter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/C1/posts", func(w http.ResponseWriter, r *http.Request) {
		w.Write(postsResponse(
			apiPost{ID: "root", ChannelID: "C1", UserID: "U1", CreateAt: 1700000000000, Message: "root post"},
			apiPost{ID: "reply", RootID: "root", ChannelID: "C1", UserID: "U2", CreateAt: 1700000001000, Message: "reply post"},
		))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1", "")
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
	if _, ok := (*docs)[0].Frontmatter["thread"]; ok {
		t.Errorf("root post should not have thread field")
	}
	if (*docs)[1].Frontmatter["thread"] != "root" {
		t.Errorf("reply thread = %v, want root", (*docs)[1].Frontmatter["thread"])
	}
}

func TestFetch_ChannelNotFoundIsSkipped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/C1/posts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1", "")
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch should fail-open on missing channel, got err: %v", err)
	}
}

func TestFetch_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/C1/posts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1", "")
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

func TestFetch_PostsOrderedByCreateAt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/channels/C1/posts", func(w http.ResponseWriter, r *http.Request) {
		w.Write(postsResponse(
			apiPost{ID: "second", ChannelID: "C1", UserID: "U1", CreateAt: 1700000002000, Message: "second"},
			apiPost{ID: "first", ChannelID: "C1", UserID: "U1", CreateAt: 1700000001000, Message: "first"},
		))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "C1", "")
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
	if (*docs)[0].ID != "mattermost:C1:first" || (*docs)[1].ID != "mattermost:C1:second" {
		t.Fatalf("docs not ordered by create_at: %+v", *docs)
	}
}
