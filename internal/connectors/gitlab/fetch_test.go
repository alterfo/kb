package gitlab

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

	full := map[string]string{
		"base_url":     srv.URL,
		"web_base_url": srv.URL,
	}
	for k, v := range cfg {
		full[k] = v
	}
	if err := c.Resolve(context.Background(), connector.Config{Name: "gl", Config: full, Secrets: secrets}, fakeEnv(env)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return c
}

func issueJSON(iid int, updated, state string) string {
	return fmt.Sprintf(`{"iid":%d,"title":"Issue %d","state":%q,"web_url":"https://example/%d","updated_at":%q,"description":"body %d","author":{"username":"octocat"},"labels":[]}`,
		iid, iid, state, iid, updated, iid)
}

func mrJSON(iid int, updated, state string) string {
	return fmt.Sprintf(`{"iid":%d,"title":"MR %d","state":%q,"web_url":"https://example/%d","updated_at":%q,"description":"body %d","author":{"username":"hubot"},"labels":[]}`,
		iid, iid, state, iid, updated, iid)
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

func TestFetch_AuthHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-pat" {
			t.Errorf("Authorization = %q, want Bearer secret-pat", got)
		}
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme", "include_wiki": "false", "include_files": "false"},
		map[string]string{"token": "GITLAB_TOKEN"}, map[string]string{"GITLAB_TOKEN": "secret-pat"})

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_ProjectsPaginationXNextPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(`[{"path_with_namespace":"acme/gizmos"}]`))
			return
		}
		w.Header().Set("X-Next-Page", "2")
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	seen := map[string]bool{}
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		seen["widgets"] = true
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fgizmos/issues", func(w http.ResponseWriter, r *http.Request) {
		seen["gizmos"] = true
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fgizmos/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme", "include_wiki": "false", "include_files": "false"}, nil, nil)

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !seen["widgets"] || !seen["gizmos"] {
		t.Fatalf("expected both projects fetched via X-Next-Page pagination, got %v", seen)
	}
}

func TestFetch_UpdatedAfterIncrementAndCursorAdvances(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	var lastUpdatedAfter string
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		lastUpdatedAfter = r.URL.Query().Get("updated_after")
		w.Write([]byte("[" + issueJSON(1, "2026-02-01T00:00:00Z", "opened") + "]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme", "include_wiki": "false", "include_files": "false"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if lastUpdatedAfter != "" {
		t.Fatalf("first fetch should not send updated_after=, got %q", lastUpdatedAfter)
	}
	if !info1.FullReconcile {
		t.Fatalf("first fetch (empty cursor) should be FullReconcile")
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
	if lastUpdatedAfter != "2026-01-31T23:59:59Z" {
		t.Fatalf("second fetch updated_after = %q, want 2026-01-31T23:59:59Z (cursor minus 1s overlap)", lastUpdatedAfter)
	}
	if info2.FullReconcile {
		t.Fatalf("second fetch (non-empty cursor) should not be FullReconcile")
	}
}

func TestFetch_CursorUnchangedOnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme", "include_wiki": "false", "include_files": "false"}, nil, nil)

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

func TestFetch_PrunePrefixesForFullyRefetchedCategories(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[" + issueJSON(1, "2026-02-01T00:00:00Z", "opened") + "]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/wikis", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"slug":"home","title":"Home","content":"# Home"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/tree", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme"}, nil, nil)

	out := make(chan connector.Document)
	_, done := drain(out)
	cursor1, info1, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch #1: %v", err)
	}
	want := []string{"acme/widgets:wiki:", "acme/widgets:file:"}
	if !sameStrings(info1.PrunePrefixes, want) {
		t.Fatalf("first fetch PrunePrefixes = %v, want %v", info1.PrunePrefixes, want)
	}
	if !info1.FullReconcile {
		t.Fatalf("first fetch (empty cursor) should be FullReconcile")
	}

	out2 := make(chan connector.Document)
	_, done2 := drain(out2)
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

func TestFetch_WikiPagesFetched(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/wikis", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"slug":"home","title":"Home","content":"# Home page"},{"slug":"setup-guide","title":"Setup Guide","content":"# Setup"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/tree", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	wikiCount := 0
	for _, d := range *docs {
		if d.Kind == "wiki" {
			wikiCount++
		}
	}
	if wikiCount != 2 {
		t.Fatalf("wiki docs = %d, want 2 (docs=%+v)", wikiCount, *docs)
	}
}

func TestFetch_WikiMissingIsSkipped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/wikis", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/tree", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme"}, nil, nil)

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch should fail-open on missing wiki, got err: %v", err)
	}
}

func TestFetch_FilesMarkdownOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/wikis", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/tree", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"type":"blob","path":"README.md","name":"README.md"},{"type":"blob","path":"logo.png","name":"logo.png"},{"type":"tree","path":"docs","name":"docs"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/files/README.md/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Widgets\n\nA great project."))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	var file *connector.Document
	for i := range *docs {
		if (*docs)[i].Kind == "file" {
			file = &(*docs)[i]
		}
	}
	if file == nil {
		t.Fatalf("expected a file document, got %+v", *docs)
	}
	if file.Title != "README.md" || file.Body != "# Widgets\n\nA great project." {
		t.Fatalf("file doc = %+v", file)
	}
}

func TestFetch_FileWithSpaceInPathUsesPathEscaping(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/wikis", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/tree", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"type":"blob","path":"docs/My File.md","name":"My File.md"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/files/docs%2FMy%20File.md/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# My File"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	for i := range *docs {
		if (*docs)[i].Kind == "file" && (*docs)[i].Body == "# My File" {
			return
		}
	}
	t.Fatalf("expected file document for path with spaces, got %+v", *docs)
}

func TestFetch_FilesPaginatesTree(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/wikis", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/tree", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(`[{"type":"blob","path":"docs/second.md","name":"second.md"}]`))
			return
		}
		w.Header().Set("X-Next-Page", "2")
		w.Write([]byte(`[{"type":"blob","path":"README.md","name":"README.md"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/files/README.md/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Widgets"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/files/docs%2Fsecond.md/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Second"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme"}, nil, nil)

	out := make(chan connector.Document)
	docs, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	files := map[string]string{}
	for i := range *docs {
		if (*docs)[i].Kind == "file" {
			files[(*docs)[i].Frontmatter["path"].(string)] = (*docs)[i].Body
		}
	}
	if files["README.md"] != "# Widgets" || files["docs/second.md"] != "# Second" {
		t.Fatalf("paginated tree files not fully indexed: %+v", files)
	}
}

func TestFetch_ExplicitProjectListSkipsGroupAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected call to group projects endpoint: %s", r.URL.Path)
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/wikis", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/repository/tree", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"projects": "acme/widgets"}, nil, nil)

	out := make(chan connector.Document)
	_, done := drain(out)
	_, _, err := c.Fetch(context.Background(), connector.Cursor{}, out)
	<-done
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetch_IssueAndMRKindClassification(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/groups/acme/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"path_with_namespace":"acme/widgets"}]`))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[" + issueJSON(1, "2026-01-05T00:00:00Z", "opened") + "]"))
	})
	mux.HandleFunc("/projects/acme%2Fwidgets/merge_requests", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[" + mrJSON(2, "2026-01-06T00:00:00Z", "merged") + "]"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestConnector(t, srv, map[string]string{"group": "acme", "include_wiki": "false", "include_files": "false"}, nil, nil)

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
	if (*docs)[0].Kind != "issue" || (*docs)[0].ID != "acme/widgets#1" {
		t.Errorf("docs[0] = %+v, want issue acme/widgets#1", (*docs)[0])
	}
	if (*docs)[1].Kind != "mr" || (*docs)[1].ID != "acme/widgets!2" {
		t.Errorf("docs[1] = %+v, want mr acme/widgets!2", (*docs)[1])
	}
}
