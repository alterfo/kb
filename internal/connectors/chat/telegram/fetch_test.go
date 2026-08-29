package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

func newTestConnector(t *testing.T, srv *httptest.Server, token string) *Connector {
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
		Name:    "tg",
		Config:  map[string]string{"base_url": srv.URL},
		Secrets: map[string]string{"token": "TG_TOKEN"},
	}
	if err := c.Resolve(context.Background(), cfg, fakeEnv(map[string]string{"TG_TOKEN": token})); err != nil {
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

func TestFetch_AuthTokenInURLPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bot123:secret/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"result":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "123:secret")
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_WrongTokenPath404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bot123:secret/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"result":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "wrong-token")
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error for wrong bot token (404)")
	}
}

func msgJSON(updateID, msgID, chatID, date int64, text, replyTo string) string {
	reply := ""
	if replyTo != "" {
		reply = `,"reply_to_message":{"message_id":` + replyTo + `}`
	}
	return `{"update_id":` + itoa(updateID) + `,"message":{"message_id":` + itoa(msgID) +
		`,"from":{"username":"alice"},"chat":{"id":` + itoa(chatID) + `,"title":"Team Chat"},"date":` +
		itoa(date) + `,"text":"` + text + `"` + reply + `}}`
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestFetch_NeverFullReconcile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bott/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"result":[` + msgJSON(1, 1, 100, 1700000000, "hello", "") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "t")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.FullReconcile {
		t.Fatal("telegram getUpdates only covers a recent window, so FullReconcile must never be reported")
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(*docs))
	}
}

func TestFetch_OffsetAdvancesAndFullReconcileFalseOnSecondFetch(t *testing.T) {
	mux := http.NewServeMux()
	var lastOffset string
	mux.HandleFunc("/bott/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		lastOffset = r.URL.Query().Get("offset")
		w.Write([]byte(`{"ok":true,"result":[` + msgJSON(5, 1, 100, 1700000000, "hello", "") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "t")
	out := make(chan connector.Document)
	_, done := drain(out)
	cursor1, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if lastOffset != "0" {
		t.Fatalf("first offset = %q, want 0", lastOffset)
	}

	out2 := make(chan connector.Document)
	_, done2 := drain(out2)
	_, info2, err := c.Fetch(context.Background(), cursor1, out2)
	<-done2
	if err != nil {
		t.Fatalf("Fetch #2: %v", err)
	}
	if lastOffset != "6" {
		t.Fatalf("second offset = %q, want 6 (update_id+1)", lastOffset)
	}
	if info2.FullReconcile {
		t.Fatal("second fetch (non-empty cursor) should not be FullReconcile")
	}
}

func TestFetch_PaginationLoopsUntilShortPage(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/bott/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			items := ""
			for i := int64(1); i <= pageLimit; i++ {
				if items != "" {
					items += ","
				}
				items += msgJSON(i, i, 100, 1700000000, "m", "")
			}
			w.Write([]byte(`{"ok":true,"result":[` + items + `]}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "t")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (full page then empty page)", calls)
	}
	if len(*docs) != pageLimit {
		t.Fatalf("docs = %d, want %d", len(*docs), pageLimit)
	}
}

func TestFetch_NonMessageUpdateSkippedButOffsetAdvances(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bott/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"result":[{"update_id":9},` + msgJSON(10, 1, 100, 1700000000, "hi", "") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "t")
	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1 (non-message update skipped)", len(*docs))
	}
	st := parseCursorState(cursor.Value)
	if st.Offset != 11 {
		t.Fatalf("offset = %d, want 11 (advanced past skipped update too)", st.Offset)
	}
}

func TestFetch_EditedUpdatesDeliveredWithEditAt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bott/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"result":[` +
			`{"update_id":1,"edited_message":{"message_id":7,"from":{"username":"alice"},"chat":{"id":100,"title":"Team Chat"},"date":1700000000,"edit_date":1700000100,"text":"fixed text"}},` +
			`{"update_id":2,"edited_channel_post":{"message_id":8,"from":{"username":"bob"},"chat":{"id":-100,"title":"News"},"date":1700000001,"edit_date":1700000101,"text":"channel edit"}}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "t")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2 (edited_message and edited_channel_post)", len(*docs))
	}
	if (*docs)[0].ID != "telegram:100:7" {
		t.Fatalf("docs[0].ID = %q, want telegram:100:7", (*docs)[0].ID)
	}
	if (*docs)[0].Frontmatter["edit_at"] != "2023-11-14T22:15:00Z" {
		t.Fatalf("docs[0] edit_at = %v, want 2023-11-14T22:15:00Z", (*docs)[0].Frontmatter["edit_at"])
	}
	if (*docs)[0].Body != "fixed text" {
		t.Fatalf("docs[0].Body = %q, want edited text", (*docs)[0].Body)
	}
	if (*docs)[1].ID != "telegram:-100:8" {
		t.Fatalf("docs[1].ID = %q, want telegram:-100:8", (*docs)[1].ID)
	}
	if (*docs)[1].Frontmatter["edit_at"] != "2023-11-14T22:15:01Z" {
		t.Fatalf("docs[1] edit_at = %v, want 2023-11-14T22:15:01Z", (*docs)[1].Frontmatter["edit_at"])
	}
}

func TestFetch_ThreadedFrontmatterOnReply(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bott/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"result":[` + msgJSON(1, 2, 100, 1700000000, "reply text", "1") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "t")
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
	if (*docs)[0].Frontmatter["thread"] != int64(1) {
		t.Fatalf("thread = %v, want 1", (*docs)[0].Frontmatter["thread"])
	}
}

func TestFetch_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bott/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "t")
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

func TestFetch_APIErrorOKFalse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bott/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, "t")
	out := make(chan connector.Document)
	_, done := drain(out)
	cursorIn := connector.Cursor{Value: "unchanged"}
	cursorOut, _, err := c.Fetch(context.Background(), cursorIn, out)
	<-done
	if err == nil {
		t.Fatal("expected error when ok:false")
	}
	if cursorOut.Value != cursorIn.Value {
		t.Fatalf("cursor changed on ok:false error")
	}
}
