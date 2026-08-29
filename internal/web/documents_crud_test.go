package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/render"
)

func getPageHx(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func deletePage(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestDocuments_DeleteSuccess(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/doomed.md", doc("doomed", "notes", "delete me please unique-token"))
	te.index(t, "notes/doomed.md")

	rr := deletePage(t, te.server.Handler(), "/documents?path=notes/doomed.md")
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want 303", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(te.root, "notes", "doomed.md")); !os.IsNotExist(err) {
		t.Errorf("document still present after delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(te.root, ".trash", "notes", "doomed.md")); err != nil {
		t.Errorf("document not moved to trash: %v", err)
	}

	rr = getPage(t, te.server.Handler(), "/search?q=unique-token")
	if strings.Contains(rr.Body.String(), "notes/doomed.md") {
		t.Errorf("deleted document still searchable")
	}
}

func TestDocuments_DeleteMissingPath(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := deletePage(t, te.server.Handler(), "/documents?path=notes/missing.md")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing path status = %d, want 404", rr.Code)
	}
}

func TestDocuments_DeleteTraversalRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := deletePage(t, te.server.Handler(), "/documents?path="+url.QueryEscape("../secret"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("traversal status = %d, want 400", rr.Code)
	}
}

func TestDocuments_DeleteNonMarkdownRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	if err := os.WriteFile(filepath.Join(te.root, "sources.yaml"), []byte("sources: []\n"), 0o644); err != nil {
		t.Fatalf("write sources.yaml: %v", err)
	}

	rr := deletePage(t, te.server.Handler(), "/documents?path=sources.yaml")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("non-markdown delete status = %d, want 400", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(te.root, "sources.yaml")); err != nil {
		t.Errorf("sources.yaml should not be deleted: %v", err)
	}
}

func TestDocuments_DeleteHtmxRedirect(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/doomed.md", doc("doomed", "notes", "body"))

	req := httptest.NewRequest(http.MethodDelete, "/documents?path=notes/doomed.md", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	te.server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("htmx delete status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("HX-Redirect"); got != "/documents" {
		t.Fatalf("HX-Redirect = %q, want /documents", got)
	}
}

func TestDocuments_EditSuccess(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/edit.md", connector.Document{
		ID: "edit", Source: "notes", Title: "Old Title", Body: "old body",
	})
	te.index(t, "notes/edit.md")

	rr := getPage(t, te.server.Handler(), "/documents/edit?path=notes/edit.md")
	if rr.Code != http.StatusOK {
		t.Fatalf("edit form status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Old Title") {
		t.Errorf("edit form missing current title")
	}
	if !strings.Contains(rr.Body.String(), "old body") {
		t.Errorf("edit form missing current body")
	}

	rr = postForm(t, te.server.Handler(), "/documents/edit", url.Values{
		"path":        {"notes/edit.md"},
		"title":       {"New Title"},
		"summary":     {"A short summary."},
		"body":        {"new body with searchable-token"},
		"frontmatter": {"topic: testing\n"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("edit status = %d, want 303", rr.Code)
	}
	loc, err := url.PathUnescape(rr.Header().Get("Location"))
	if err != nil {
		t.Fatalf("unescaping redirect location: %v", err)
	}
	if !strings.Contains(loc, "/documents/view?path=notes/edit.md") {
		t.Errorf("redirect location = %q", rr.Header().Get("Location"))
	}

	raw, err := os.ReadFile(filepath.Join(te.root, "notes", "edit.md"))
	if err != nil {
		t.Fatalf("reading edited document: %v", err)
	}
	parsed, err := render.Parse(raw)
	if err != nil {
		t.Fatalf("parsing edited document: %v", err)
	}
	if parsed.Title != "New Title" {
		t.Errorf("title = %q, want New Title", parsed.Title)
	}
	if parsed.Summary != "A short summary." {
		t.Errorf("summary = %q, want A short summary.", parsed.Summary)
	}
	if parsed.Body != "new body with searchable-token" {
		t.Errorf("body = %q, want new body with searchable-token", parsed.Body)
	}
	if got := parsed.Frontmatter["topic"]; got != "testing" {
		t.Errorf("frontmatter topic = %v, want testing", got)
	}

	rr = getPage(t, te.server.Handler(), "/search?q=searchable-token")
	if !strings.Contains(rr.Body.String(), "notes/edit.md") {
		t.Errorf("edited document not searchable by new body")
	}
}

func TestDocuments_EditPreservesFrontmatterTypes(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/typed.md", connector.Document{
		ID: "typed", Source: "notes", Title: "Typed", Body: "body",
		Frontmatter: map[string]any{
			"merged": true,
			"number": 563,
			"labels": []any{"a", "b"},
		},
	})

	rr := getPage(t, te.server.Handler(), "/documents/edit?path=notes/typed.md")
	if rr.Code != http.StatusOK {
		t.Fatalf("edit form status = %d, want 200", rr.Code)
	}

	rr = postForm(t, te.server.Handler(), "/documents/edit", url.Values{
		"path":        {"notes/typed.md"},
		"title":       {"Typed"},
		"summary":     {""},
		"body":        {"body updated"},
		"frontmatter": {"merged: true\nnumber: 563\nlabels:\n  - a\n  - b\n"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("edit status = %d, want 303; body=%s", rr.Code, rr.Body.String())
	}

	raw, err := os.ReadFile(filepath.Join(te.root, "notes", "typed.md"))
	if err != nil {
		t.Fatalf("reading edited document: %v", err)
	}
	parsed, err := render.Parse(raw)
	if err != nil {
		t.Fatalf("parsing edited document: %v", err)
	}
	if merged, ok := parsed.Frontmatter["merged"].(bool); !ok || !merged {
		t.Errorf("merged = %#v, want bool true", parsed.Frontmatter["merged"])
	}
	if number, ok := parsed.Frontmatter["number"].(int); !ok || number != 563 {
		t.Errorf("number = %#v, want int 563", parsed.Frontmatter["number"])
	}
	labels, ok := parsed.Frontmatter["labels"].([]any)
	if !ok || len(labels) != 2 || labels[0] != "a" || labels[1] != "b" {
		t.Errorf("labels = %#v, want [a b]", parsed.Frontmatter["labels"])
	}
}

func TestDocuments_EditValidation(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/edit.md", doc("edit", "notes", "old body"))

	cases := []struct {
		name   string
		form   url.Values
		status int
	}{
		{"missing path", url.Values{"body": {"x"}}, http.StatusBadRequest},
		{"traversal", url.Values{"path": {"../secret"}, "body": {"x"}}, http.StatusBadRequest},
		{"empty body", url.Values{"path": {"notes/edit.md"}, "body": {"   "}}, http.StatusBadRequest},
		{"bad frontmatter", url.Values{"path": {"notes/edit.md"}, "body": {"x"}, "frontmatter": {"no-equals-sign"}}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := postForm(t, te.server.Handler(), "/documents/edit", tc.form)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d", rr.Code, tc.status)
			}
		})
	}
}

func TestDocuments_EditFormMissingFile(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/documents/edit?path=notes/missing.md")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("edit form missing status = %d, want 404", rr.Code)
	}
}

func TestDocuments_Pagination(t *testing.T) {
	te := newTestEnv(t, nil)
	for _, name := range []string{"a", "b", "c"} {
		writeDoc(t, te.root, "notes/"+name+".md", doc(name, "notes", "body "+name))
	}

	rr := getPage(t, te.server.Handler(), "/documents?limit=2")
	body := rr.Body.String()
	for _, want := range []string{"notes/a.md", "notes/b.md", "showing 1–2 of 3"} {
		if !strings.Contains(body, want) {
			t.Errorf("first page missing %q", want)
		}
	}
	if strings.Contains(body, "notes/c.md") {
		t.Errorf("first page leaked notes/c.md")
	}
	if !strings.Contains(body, `hx-get="/documents?limit=2&amp;offset=2"`) && !strings.Contains(body, `hx-get="/documents?limit=2&offset=2"`) {
		t.Errorf("first page missing next-page link, got %q", body)
	}

	rr = getPage(t, te.server.Handler(), "/documents?limit=2&offset=2")
	body = rr.Body.String()
	if !strings.Contains(body, "notes/c.md") {
		t.Errorf("second page missing notes/c.md")
	}
	if strings.Contains(body, "notes/a.md") {
		t.Errorf("second page leaked notes/a.md")
	}
	if !strings.Contains(body, "showing 3–3 of 3") {
		t.Errorf("second page missing range")
	}
}

func TestDocuments_HtmxFragment(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/a.md", doc("a", "notes", "body a"))

	rr := getPage(t, te.server.Handler(), "/documents")
	if !strings.Contains(rr.Body.String(), "<html") {
		t.Errorf("full page missing <html>")
	}

	rr = getPageHx(t, te.server.Handler(), "/documents")
	body := rr.Body.String()
	if strings.Contains(body, "<html") {
		t.Errorf("htmx fragment should not contain <html>")
	}
	if !strings.Contains(body, `<div id="documents-table">`) {
		t.Errorf("htmx fragment missing table wrapper")
	}
	if !strings.Contains(body, "notes/a.md") {
		t.Errorf("htmx fragment missing document row")
	}
}

func TestDocuments_SummaryColumnAndBadge(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/summary.md", connector.Document{
		ID: "sum", Source: "notes", Title: "Summarized", Body: "body", Summary: "Short summary.",
	})
	writeDoc(t, te.root, "notes/plain.md", connector.Document{
		ID: "plain", Source: "notes", Title: "Plain", Body: "body",
	})

	rr := getPage(t, te.server.Handler(), "/documents")
	body := rr.Body.String()
	if !strings.Contains(body, "Documents (2)") {
		t.Errorf("missing total counter")
	}
	if !strings.Contains(body, "Short summary.") {
		t.Errorf("missing summary text")
	}
	if !strings.Contains(body, "no description") {
		t.Errorf("missing no-description badge")
	}
}
