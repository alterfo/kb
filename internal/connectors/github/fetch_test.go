package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	c.sleep = func(ctx context.Context, d time.Duration) error { return nil }
	c.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	full := map[string]string{
		"base_url":     srv.URL,
		"web_base_url": srv.URL,
		"raw_base_url": srv.URL,
	}
	for k, v := range cfg {
		full[k] = v
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "gh", Config: full, Secrets: secrets}, fakeEnv(env)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return c
}

func issuesJSON(items ...string) string {
	return "[" + joinStrings(items, ",") + "]"
}

func joinStrings(items []string, sep string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += sep
		}
		out += it
	}
	return out
}

func issueJSON(number int, updated, state string) string {
	return fmt.Sprintf(`{"number":%d,"title":"Issue %d","state":%q,"html_url":"https://example/%d","updated_at":%q,"body":"body %d","user":{"login":"octocat"},"labels":[]}`,
		number, number, state, number, updated, number)
}

func prJSON(number int, updated string, mergedAt string) string {
	merged := "null"
	if mergedAt != "" {
		merged = strconv.Quote(mergedAt)
	}
	return fmt.Sprintf(`{"number":%d,"title":"PR %d","state":"closed","html_url":"https://example/%d","updated_at":%q,"body":"body %d","user":{"login":"hubot"},"labels":[],"pull_request":{"merged_at":%s}}`,
		number, number, number, updated, number, merged)
}

func TestFetch_AuthAndAcceptHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-pat" {
			t.Errorf("Authorization = %q, want Bearer secret-pat", got)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want application/vnd.github+json", got)
		}
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme", "include_wiki": "false", "include_contents": "false"},
		map[string]string{"token": "GITHUB_TOKEN"}, map[string]string{"GITHUB_TOKEN": "secret-pat"})

	out := make(chan connector.Document)
	go func() {
		for range out {
		}
	}()
	if _, _, err := c.Fetch(context.Background(), connector.Cursor{}, out); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_ReposPaginationLinkHeader(t *testing.T) {
	mux := http.NewServeMux()
	var page1URL string
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "2" {
			w.Write([]byte(`[{"full_name":"acme/gizmos"}]`))
			return
		}
		page1URL = "http://" + r.Host + "/orgs/acme/repos?cursor=2"
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, page1URL))
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	seen := map[string]bool{}
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		seen["widgets"] = true
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/repos/acme/gizmos/issues", func(w http.ResponseWriter, r *http.Request) {
		seen["gizmos"] = true
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme", "include_wiki": "false", "include_contents": "false"}, nil, nil)

	out := make(chan connector.Document)
	go func() {
		for range out {
		}
	}()
	if _, _, err := c.Fetch(context.Background(), connector.Cursor{}, out); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !seen["widgets"] || !seen["gizmos"] {
		t.Fatalf("expected both repos fetched via pagination, got %v", seen)
	}
}

func TestFetch_SinceIncrementAndCursorAdvances(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	var lastSince string
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		lastSince = r.URL.Query().Get("since")
		w.Write([]byte("[" + issueJSON(1, "2026-02-01T00:00:00Z", "open") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme", "include_wiki": "false", "include_contents": "false"}, nil, nil)

	out := make(chan connector.Document)
	var docs []connector.Document
	done := make(chan struct{})
	go func() {
		for d := range out {
			docs = append(docs, d)
		}
		close(done)
	}()
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if lastSince != "" {
		t.Fatalf("first fetch should not send since=, got %q", lastSince)
	}
	if !info1.FullReconcile {
		t.Fatalf("first fetch (empty cursor) should be FullReconcile")
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}

	out2 := make(chan connector.Document)
	done2 := make(chan struct{})
	go func() {
		for range out2 {
		}
		close(done2)
	}()
	_, info2, err := c.Fetch(context.Background(), cursor1, out2)
	<-done2
	if err != nil {
		t.Fatalf("Fetch #2: %v", err)
	}
	if lastSince != "2026-01-31T23:59:59Z" {
		t.Fatalf("second fetch since = %q, want 2026-01-31T23:59:59Z (cursor minus 1s overlap)", lastSince)
	}
	if info2.FullReconcile {
		t.Fatalf("second fetch (non-empty cursor) should not be FullReconcile")
	}
}

func TestFetch_PrunePrefixesForFullyRefetchedCategories(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(issuesJSON(issueJSON(1, "2026-02-01T00:00:00Z", "open"))))
	})
	mux.HandleFunc("/repos/acme/widgets/contents", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/acme/widgets/wiki", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><a href="/acme/widgets/wiki/Home">Home</a></html>`))
	})
	mux.HandleFunc("/wiki/acme/widgets/Home.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Home"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme"}, nil, nil)

	out := make(chan connector.Document)
	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch #1: %v", err)
	}
	want := []string{"acme/widgets:contents:", "acme/widgets:wiki:"}
	if !sameStrings(info1.PrunePrefixes, want) {
		t.Fatalf("first fetch PrunePrefixes = %v, want %v", info1.PrunePrefixes, want)
	}
	if !info1.FullReconcile {
		t.Fatalf("first fetch (empty cursor) should be FullReconcile")
	}

	out2 := make(chan connector.Document)
	done2 := make(chan struct{})
	go func() {
		for range out2 {
		}
		close(done2)
	}()
	_, info2, err := c.Fetch(context.Background(), cursor1, out2)
	<-done2
	if err != nil {
		t.Fatalf("Fetch #2: %v", err)
	}
	if info2.FullReconcile {
		t.Fatalf("second fetch should not be FullReconcile")
	}
	if !sameStrings(info2.PrunePrefixes, want) {
		t.Fatalf("second fetch PrunePrefixes = %v, want %v", info2.PrunePrefixes, want)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFetch_ReposETag304ReusesKnownRepos(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme", "include_wiki": "false", "include_contents": "false"}, nil, nil)

	out := make(chan connector.Document)
	go func() {
		for range out {
		}
	}()
	cursor1, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	if err != nil {
		t.Fatalf("Fetch #1: %v", err)
	}

	out2 := make(chan connector.Document)
	go func() {
		for range out2 {
		}
	}()
	cursor2, _, err := c.Fetch(context.Background(), cursor1, out2)
	if err != nil {
		t.Fatalf("Fetch #2: %v", err)
	}
	if calls != 2 {
		t.Fatalf("repos endpoint calls = %d, want 2", calls)
	}
	st := parseCursorState(cursor2.Value)
	if len(st.Repos) != 1 || st.Repos[0] != "acme/widgets" {
		t.Fatalf("cursor repos = %v, want [acme/widgets] reused from 304", st.Repos)
	}
}

func TestFetch_RateLimit403RetriesThenSucceeds(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	calls := 0
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme", "include_wiki": "false", "include_contents": "false"}, nil, nil)

	out := make(chan connector.Document)
	go func() {
		for range out {
		}
	}()
	if _, _, err := c.Fetch(context.Background(), connector.Cursor{}, out); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (2 rate-limited + 1 success)", calls)
	}
}

func TestFetch_RateLimit403ExhaustsRetriesAndFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme", "include_wiki": "false", "include_contents": "false"}, nil, nil)
	c.maxRateLimitRetries = 2

	out := make(chan connector.Document)
	go func() {
		for range out {
		}
	}()
	cursorIn := connector.Cursor{Value: "should-not-change"}
	cursorOut, _, err := c.Fetch(context.Background(), cursorIn, out)
	if err == nil {
		t.Fatal("expected error after exhausting rate-limit retries")
	}
	if cursorOut.Value != cursorIn.Value {
		t.Fatalf("cursor rolled forward on error: got %q, want unchanged %q", cursorOut.Value, cursorIn.Value)
	}
}

func TestFetch_WikiPagesDiscoveredAndFetched(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/repos/acme/widgets/contents", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/acme/widgets/wiki", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><a href="/acme/widgets/wiki/Home">Home</a><a href="/acme/widgets/wiki/Setup-Guide">Setup</a></html>`))
	})
	mux.HandleFunc("/wiki/acme/widgets/Home.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Home page"))
	})
	mux.HandleFunc("/wiki/acme/widgets/Setup-Guide.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Setup"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme"}, nil, nil)

	out := make(chan connector.Document)
	var docs []connector.Document
	done := make(chan struct{})
	go func() {
		for d := range out {
			docs = append(docs, d)
		}
		close(done)
	}()
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	wikiCount := 0
	for _, d := range docs {
		if d.Kind == "wiki" {
			wikiCount++
		}
	}
	if wikiCount != 2 {
		t.Fatalf("wiki docs = %d, want 2 (docs=%+v)", wikiCount, docs)
	}
}

func TestFetch_WikiMissingIsSkipped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/repos/acme/widgets/contents", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/acme/widgets/wiki", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme"}, nil, nil)

	out := make(chan connector.Document)
	go func() {
		for range out {
		}
	}()
	if _, _, err := c.Fetch(context.Background(), connector.Cursor{}, out); err != nil {
		t.Fatalf("Fetch should fail-open on missing wiki, got err: %v", err)
	}
}

func TestFetch_ContentsMarkdownFiles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/repos/acme/widgets/contents", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf(`[
			{"type":"file","name":"README.md","path":"README.md","download_url":"http://%s/raw/README.md"},
			{"type":"file","name":"logo.png","path":"logo.png","download_url":"http://%s/raw/logo.png"},
			{"type":"dir","name":"docs","path":"docs"}
		]`, r.Host, r.Host)))
	})
	mux.HandleFunc("/raw/README.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Widgets\n\nA great project."))
	})
	mux.HandleFunc("/acme/widgets/wiki", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme"}, nil, nil)

	out := make(chan connector.Document)
	var docs []connector.Document
	done := make(chan struct{})
	go func() {
		for d := range out {
			docs = append(docs, d)
		}
		close(done)
	}()
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	var content *connector.Document
	for i := range docs {
		if docs[i].Kind == "content" {
			content = &docs[i]
		}
	}
	if content == nil {
		t.Fatalf("expected a content document, got %+v", docs)
	}
	if content.Title != "README.md" || content.Body != "# Widgets\n\nA great project." {
		t.Fatalf("content doc = %+v", content)
	}
}

func TestFetch_ContentsRecursesIntoSubdirs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/repos/acme/widgets/contents", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf(`[
			{"type":"file","name":"README.md","path":"README.md","download_url":"http://%s/raw/README.md"},
			{"type":"dir","name":"docs","path":"docs"}
		]`, r.Host)))
	})
	mux.HandleFunc("/repos/acme/widgets/contents/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf(`[
			{"type":"file","name":"guide.md","path":"docs/guide.md","download_url":"http://%s/raw/docs/guide.md"},
			{"type":"file","name":"diagram.png","path":"docs/diagram.png","download_url":"http://%s/raw/docs/diagram.png"}
		]`, r.Host, r.Host)))
	})
	mux.HandleFunc("/raw/README.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Widgets\n\nA great project."))
	})
	mux.HandleFunc("/raw/docs/guide.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Guide\n\nDeep in the tree."))
	})
	mux.HandleFunc("/acme/widgets/wiki", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme"}, nil, nil)

	out := make(chan connector.Document)
	var docs []connector.Document
	done := make(chan struct{})
	go func() {
		for d := range out {
			docs = append(docs, d)
		}
		close(done)
	}()
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	byPath := map[string]string{}
	for _, d := range docs {
		if d.Kind == "content" {
			byPath[d.Frontmatter["path"].(string)] = d.Body
		}
	}
	if byPath["README.md"] != "# Widgets\n\nA great project." {
		t.Fatalf("root README not indexed: %+v", byPath)
	}
	if byPath["docs/guide.md"] != "# Guide\n\nDeep in the tree." {
		t.Fatalf("subdirectory guide not indexed: %+v", byPath)
	}
}

func TestFetch_ExplicitRepoListSkipsReposAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected call to org repos endpoint: %s", r.URL.Path)
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/repos/acme/widgets/contents", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/acme/widgets/wiki", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"repos": "acme/widgets"}, nil, nil)

	out := make(chan connector.Document)
	go func() {
		for range out {
		}
	}()
	if _, _, err := c.Fetch(context.Background(), connector.Cursor{}, out); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_IssueVsPRKindClassification(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"full_name":"acme/widgets"}]`))
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(issuesJSON(
			issueJSON(1, "2026-01-05T00:00:00Z", "open"),
			prJSON(2, "2026-01-06T00:00:00Z", "2026-01-06T00:00:00Z"),
		)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"org": "acme", "include_wiki": "false", "include_contents": "false"}, nil, nil)

	out := make(chan connector.Document)
	var docs []connector.Document
	done := make(chan struct{})
	go func() {
		for d := range out {
			docs = append(docs, d)
		}
		close(done)
	}()
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	if docs[0].Kind != "issue" {
		t.Errorf("docs[0].Kind = %q, want issue", docs[0].Kind)
	}
	if docs[1].Kind != "pr" || docs[1].Frontmatter["merged"] != true {
		t.Errorf("docs[1] = %+v, want pr with merged=true", docs[1])
	}
}
