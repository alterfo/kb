package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/transport"
)

func newTestConnector(t *testing.T, srv *httptest.Server, token string, opts ...func(*transport.Config)) *Connector {
	t.Helper()
	c := New()
	cfg := transport.Config{
		Doer:       srv.Client(),
		MaxRetries: 2,
		BaseDelay:  time.Millisecond,
		MaxDelay:   5 * time.Millisecond,
		Sleep:      func(ctx context.Context, d time.Duration) error { return nil },
		JitterFunc: func() float64 { return 1 },
	}
	for _, o := range opts {
		o(&cfg)
	}
	c.client = transport.NewClient(cfg)
	connectorCfg := connector.Config{
		Name:    "ds",
		Config:  map[string]string{"channels": "C1", "guild_id": "G1", "base_url": srv.URL},
		Secrets: map[string]string{"token": "DISCORD_TOKEN"},
	}
	if err := c.Resolve(context.Background(), connectorCfg, fakeEnv(map[string]string{"DISCORD_TOKEN": token})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return c
}

func drain(out chan connector.Document) (*[]connector.Document, <-chan struct{}) {
	docs := []connector.Document{}
	done := make(chan struct{})
	go func() {
		for d := range out {
			docs = append(docs, d)
		}
		close(done)
	}()
	return &docs, done
}

func messageJSON(id int64, content, timestamp string, replyTo string) string {
	m := map[string]any{
		"id":         strconv.FormatInt(id, 10),
		"channel_id": "C1",
		"author":     map[string]string{"id": "u1", "username": "alice"},
		"content":    content,
		"timestamp":  timestamp,
	}
	if replyTo != "" {
		m["referenced_message"] = map[string]any{
			"id":         replyTo,
			"channel_id": "C1",
			"author":     map[string]string{"id": "u1", "username": "alice"},
			"content":    "root",
			"timestamp":  timestamp,
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestFetch_AuthBotTokenAndKind(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if auth != "Bot secret" {
		t.Fatalf("Authorization = %q, want Bot secret", auth)
	}
	if len(*docs) != 0 {
		t.Fatalf("docs = %d, want 0", len(*docs))
	}
}

func TestFetch_EmptyChannelDoesNotKeepFullReconcile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if len(*docs) != 0 {
		t.Fatalf("first docs = %d, want 0", len(*docs))
	}
	if !info.FullReconcile {
		t.Fatal("first empty-cursor fetch must be FullReconcile")
	}
	if st := parseCursorState(cursor.Value); st.Channels["C1"] != "" {
		t.Fatalf("cursor = %v, want empty C1 persisted", st.Channels)
	}

	out2 := make(chan connector.Document)
	docs2, done2 := drain(out2)
	cursor2, info2, err := c.Fetch(context.Background(), cursor, out2)
	<-done2
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if len(*docs2) != 0 {
		t.Fatalf("second docs = %d, want 0", len(*docs2))
	}
	if info2.FullReconcile {
		t.Fatal("empty channel must not keep triggering FullReconcile")
	}
	if st := parseCursorState(cursor2.Value); st.Channels["C1"] != "" {
		t.Fatalf("second cursor = %v, want empty C1 persisted", st.Channels)
	}
}

func TestFetch_PaginationBeforeUntilEmpty(t *testing.T) {
	var before string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		before = r.URL.Query().Get("before")
		if calls == 1 {
			items := make([]string, 0, pageLimit)
			for i := 100; i >= 1; i-- {
				items = append(items, messageJSON(int64(i), "message "+strconv.Itoa(i), "2026-08-22T10:00:00Z", ""))
			}
			w.Write([]byte("[" + strings.Join(items, ",") + "]"))
			return
		}
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor, info, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (full page then empty page)", calls)
	}
	if before != "1" {
		t.Fatalf("second before = %q, want oldest id 1", before)
	}
	if len(*docs) != pageLimit {
		t.Fatalf("docs = %d, want %d", len(*docs), pageLimit)
	}
	if !info.FullReconcile {
		t.Fatal("first empty-cursor fetch must be FullReconcile")
	}
	st := parseCursorState(cursor.Value)
	if st.Channels["C1"] != "100" {
		t.Fatalf("cursor = %q, want newest id 100", st.Channels["C1"])
	}
}

func TestFetch_IncrementalAfterCursor(t *testing.T) {
	var after string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after = r.URL.Query().Get("after")
		w.Write([]byte("[" + messageJSON(105, "new", "2026-08-22T11:00:00Z", "") + "]"))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	since := connector.Cursor{Value: cursorState{Channels: map[string]string{"C1": "104"}}.encode()}
	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor, info, err := c.Fetch(context.Background(), since, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if after != "104" {
		t.Fatalf("after = %q, want 104", after)
	}
	if info.FullReconcile {
		t.Fatal("non-empty cursor must not be FullReconcile")
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(*docs))
	}
	st := parseCursorState(cursor.Value)
	if st.Channels["C1"] != "105" {
		t.Fatalf("cursor = %q, want 105", st.Channels["C1"])
	}
}

func TestFetch_IdleChannelPreservesCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	since := connector.Cursor{Value: cursorState{Channels: map[string]string{"C1": "104"}}.encode()}
	out := make(chan connector.Document)
	_, done := drain(out)
	cursor, info, err := c.Fetch(context.Background(), since, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if info.FullReconcile {
		t.Fatal("idle channel with an existing cursor must not trigger FullReconcile")
	}
	st := parseCursorState(cursor.Value)
	if st.Channels["C1"] != "104" {
		t.Fatalf("cursor = %q, want preserved 104", st.Channels["C1"])
	}
}

func TestFetch_IncrementalMultiPageSwitchesToBefore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after := r.URL.Query().Get("after")
		before := r.URL.Query().Get("before")
		var ids []int64
		switch {
		case after == "100":
			for i := int64(250); i >= 151; i-- {
				ids = append(ids, i)
			}
		case before == "151":
			for i := int64(150); i >= 51; i-- {
				ids = append(ids, i)
			}
		}
		items := make([]string, 0, len(ids))
		for _, id := range ids {
			items = append(items, messageJSON(id, "m", "2026-08-22T11:00:00Z", ""))
		}
		w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	since := connector.Cursor{Value: cursorState{Channels: map[string]string{"C1": "100"}}.encode()}
	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor, _, err := c.Fetch(context.Background(), since, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 150 {
		t.Fatalf("docs = %d, want 150", len(*docs))
	}
	st := parseCursorState(cursor.Value)
	if st.Channels["C1"] != "250" {
		t.Fatalf("cursor = %q, want newest 250", st.Channels["C1"])
	}
	seen := map[string]bool{}
	for _, d := range *docs {
		if seen[d.ID] {
			t.Fatalf("duplicate document %q", d.ID)
		}
		seen[d.ID] = true
	}
}

func TestFetch_ReturnsOriginalCursorOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	since := connector.Cursor{Value: cursorState{Channels: map[string]string{"C1": "100"}}.encode()}
	out := make(chan connector.Document)
	docs, done := drain(out)
	got, _, err := c.Fetch(context.Background(), since, out)
	<-done
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if got.Value != since.Value {
		t.Fatalf("cursor = %q, want original %q (rollback)", got.Value, since.Value)
	}
	if len(*docs) != 0 {
		t.Fatalf("docs = %d, want 0 on error", len(*docs))
	}
}

func TestFetch_NotFoundReturnsEmptyAndNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(*docs) != 0 {
		t.Fatalf("docs = %d, want 0", len(*docs))
	}
	if st := parseCursorState(cursor.Value); len(st.Channels) != 1 || st.Channels["C1"] != "" {
		t.Fatalf("cursor channels = %v, want C1 persisted with an empty cursor", st.Channels)
	}
}

func TestFetch_ChannelSetChangeRefetchesUnchangedChannels(t *testing.T) {
	var c1After, c1Before string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channels/C1/messages":
			c1After = r.URL.Query().Get("after")
			c1Before = r.URL.Query().Get("before")
			w.Write([]byte("[" + messageJSON(300, "oldest", "2026-08-22T09:00:00Z", "") + "]"))
		case "/channels/C2/messages":
			w.Write([]byte("[" + messageJSON(1, "new", "2026-08-22T10:00:00Z", "") + "]"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New()
	c.client = transport.NewClient(transport.Config{Doer: srv.Client()})
	connectorCfg := connector.Config{
		Name:    "ds",
		Config:  map[string]string{"channels": "C1,C2", "guild_id": "G1", "base_url": srv.URL},
		Secrets: map[string]string{"token": "DISCORD_TOKEN"},
	}
	if err := c.Resolve(context.Background(), connectorCfg, fakeEnv(map[string]string{"DISCORD_TOKEN": "secret"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	since := connector.Cursor{Value: cursorState{Channels: map[string]string{"C1": "100"}}.encode()}
	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor, info, err := c.Fetch(context.Background(), since, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !info.FullReconcile {
		t.Fatal("channel-set change must be FullReconcile")
	}
	if c1After != "" || c1Before != "" {
		t.Fatalf("C1 fetched with after=%q before=%q, want full re-enumeration (no cursor params)", c1After, c1Before)
	}
	st := parseCursorState(cursor.Value)
	if st.Channels["C1"] != "300" || st.Channels["C2"] != "1" {
		t.Fatalf("cursor = %v, want C1=300 C2=1", st.Channels)
	}
	if len(*docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(*docs))
	}
}

func TestFetch_RemovedChannelCursorDropped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[" + messageJSON(105, "new", "2026-08-22T11:00:00Z", "") + "]"))
	}))
	defer srv.Close()

	c := New()
	c.client = transport.NewClient(transport.Config{Doer: srv.Client()})
	connectorCfg := connector.Config{
		Name:    "ds",
		Config:  map[string]string{"channels": "C1", "guild_id": "G1", "base_url": srv.URL},
		Secrets: map[string]string{"token": "DISCORD_TOKEN"},
	}
	if err := c.Resolve(context.Background(), connectorCfg, fakeEnv(map[string]string{"DISCORD_TOKEN": "secret"})); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	since := connector.Cursor{Value: cursorState{Channels: map[string]string{"C1": "100", "C2": "200"}}.encode()}
	out := make(chan connector.Document)
	_, done := drain(out)
	cursor, info, err := c.Fetch(context.Background(), since, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !info.FullReconcile {
		t.Fatal("removed channel must trigger FullReconcile")
	}
	st := parseCursorState(cursor.Value)
	if _, ok := st.Channels["C2"]; ok {
		t.Fatalf("cursor still contains removed channel C2: %v", st.Channels)
	}
	if st.Channels["C1"] != "105" {
		t.Fatalf("C1 cursor = %q, want 105", st.Channels["C1"])
	}
}

func TestFetch_RetryOn429ThenSuccessWithFakeClock(t *testing.T) {
	var slept []time.Duration
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("[" + messageJSON(1, "ok", "2026-08-22T10:00:00Z", "") + "]"))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret", func(cfg *transport.Config) {
		cfg.Sleep = func(ctx context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		}
	})
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (429 then success)", calls)
	}
	if len(*docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(*docs))
	}
	if len(slept) == 0 || slept[0] <= 0 {
		t.Fatalf("expected positive retry sleep, got %v", slept)
	}
}

func TestFetch_429ExhaustsRetriesWithError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected error after 429 retries are exhausted")
	}
	if len(*docs) != 0 {
		t.Fatalf("docs = %d, want 0", len(*docs))
	}
}

func TestFetch_TruncatedHistoryIsError(t *testing.T) {
	old := maxPages
	maxPages = 2
	defer func() { maxPages = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := make([]string, 0, pageLimit)
		for i := 100; i >= 1; i-- {
			items = append(items, messageJSON(int64(i), "m", "2026-08-22T10:00:00Z", ""))
		}
		w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err == nil {
		t.Fatal("expected truncation error when maxPages is exhausted")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("error = %q, want truncated marker", err)
	}
}

func TestFetch_RemovedChannelTriggersFullReconcile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := newTestConnector(t, srv, "secret")
	since := connector.Cursor{Value: cursorState{Channels: map[string]string{"C1": "x", "C2": "y"}}.encode()}
	out := make(chan connector.Document)
	docs, done := drain(out)
	_, info, err := c.Fetch(context.Background(), since, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !info.FullReconcile {
		t.Fatal("removed channel must trigger FullReconcile so orphans are pruned")
	}
	if len(*docs) != 0 {
		t.Fatalf("docs = %d, want 0", len(*docs))
	}
}
