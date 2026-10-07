package searxng

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(Config{BaseURL: srv.URL, Doer: srv.Client()})
	return srv, c
}

func TestSearchSuccess(t *testing.T) {
	body := `{"results":[
		{"title":"<b>First</b> result","url":"https://a.example.com/1","content":"<em>alpha</em> &amp; beta"},
		{"title":"Second result","url":"https://a.example.com/2","content":"gamma delta"},
		{"title":"Third result","url":"https://a.example.com/3","content":"domain cap"},
		{"title":"Fourth result","url":"https://b.example.com/1","content":"other domain"},
		{"title":"Bad link","url":"ftp://b.example.com/file","content":"skipped"}
	]}`
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			t.Fatalf("format = %q, want json", r.URL.Query().Get("format"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})

	results, err := c.Search(context.Background(), "some query", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if results[0].Title != "First result" {
		t.Fatalf("title = %q, want HTML stripped", results[0].Title)
	}
	if results[0].Snippet != "alpha & beta" {
		t.Fatalf("snippet = %q, want entities unescaped", results[0].Snippet)
	}
	if results[0].URL != "https://a.example.com/1" {
		t.Fatalf("url = %q", results[0].URL)
	}
	if results[1].URL != "https://a.example.com/2" {
		t.Fatalf("second = %q, want same-domain second slot", results[1].URL)
	}
	if results[2].URL != "https://b.example.com/1" {
		t.Fatalf("third = %q, want next domain after per-domain cap", results[2].URL)
	}
}

func TestSearchRespectsN(t *testing.T) {
	body := `{"results":[
		{"title":"a","url":"https://a.example.com/1","content":"x"},
		{"title":"b","url":"https://b.example.com/1","content":"x"},
		{"title":"c","url":"https://c.example.com/1","content":"x"}
	]}`
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	})

	results, err := c.Search(context.Background(), "q", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
}

func TestSearchNon200(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	_, err := c.Search(context.Background(), "q", 5)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v, want status error", err)
	}
}

func TestSearchInvalidJSON(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json"))
	})

	_, err := c.Search(context.Background(), "q", 5)
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, want decode error", err)
	}
}

func TestSearchHugeResponse(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"title":"` + strings.Repeat("x", 256) + `","url":"https://a.example.com","content":"x"}]}`))
	})
	c.maxResponseBytes = 64

	_, err := c.Search(context.Background(), "q", 5)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
}

func TestSearchTimeout(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	})
	c.timeout = 30 * time.Millisecond

	start := time.Now()
	_, err := c.Search(context.Background(), "q", 5)
	if err == nil {
		t.Fatal("want timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Search took %v, want quick timeout", elapsed)
	}
}

func TestSearchDisabled(t *testing.T) {
	c := New(Config{})
	if c.Enabled() {
		t.Fatal("empty BaseURL must be disabled")
	}
	_, err := c.Search(context.Background(), "q", 5)
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
}

func TestNormalizeResultTruncates(t *testing.T) {
	raw := rawResult{
		Title:   strings.Repeat("t", maxTitleRunes+10),
		URL:     "https://example.com/" + strings.Repeat("p", maxURLRunes+10),
		Content: strings.Repeat("s", maxSnippetRunes+10),
	}
	r, ok := normalizeResult(raw)
	if !ok {
		t.Fatal("normalizeResult rejected valid input")
	}
	if len([]rune(r.Title)) != maxTitleRunes {
		t.Fatalf("title runes = %d, want %d", len([]rune(r.Title)), maxTitleRunes)
	}
	if len([]rune(r.URL)) != maxURLRunes {
		t.Fatalf("url runes = %d, want %d", len([]rune(r.URL)), maxURLRunes)
	}
	if len([]rune(r.Snippet)) != maxSnippetRunes {
		t.Fatalf("snippet runes = %d, want %d", len([]rune(r.Snippet)), maxSnippetRunes)
	}
}

func TestNormalizeResultRejectsNonHTTP(t *testing.T) {
	if _, ok := normalizeResult(rawResult{URL: "javascript:alert(1)", Title: "x"}); ok {
		t.Fatal("javascript URL must be rejected")
	}
	if _, ok := normalizeResult(rawResult{URL: "", Title: "x"}); ok {
		t.Fatal("empty URL must be rejected")
	}
}

func TestStripHTML(t *testing.T) {
	cases := map[string]string{
		"<p>Hello <b>world</b></p>": "Hello world",
		"a &amp; b":                 "a & b",
		"  spaced\n\tout  ":         "spaced out",
	}
	for in, want := range cases {
		if got := stripHTML(in); got != want {
			t.Errorf("stripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}
