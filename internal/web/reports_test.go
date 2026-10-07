package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/store/graphstore"
)

func TestReports_SearchSynthesisFallsBackWithoutLLM(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/r1.md", doc("r1", "notes", "report retrievable content"))
	te.index(t, "notes/r1.md")

	rr := postForm(t, te.server.Handler(), "/reports", url.Values{"mode": {"search"}, "q": {"report retrievable"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "notes/r1.md") && !strings.Contains(body, "r1.md") {
		t.Errorf("search report missing fallback listing: %q", body)
	}
}

func TestReports_GlobalFallsBackWithoutLLM(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "c1", Title: "Community Alpha", Summary: "alpha summary"},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	rr := postForm(t, te.server.Handler(), "/reports", url.Values{"mode": {"global"}, "q": {"anything"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Community Alpha") {
		t.Errorf("global report missing community fallback: %q", rr.Body.String())
	}
}

func TestReports_DiagramRendersMermaidWithoutLLM(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e:web", Name: "Web", Type: "component", SourceChunks: []string{"c1"}},
		{ID: "e:db", Name: "Database", Type: "storage", SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r:1", Src: "e:web", Dst: "e:db", Type: "reads", SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	rr := postForm(t, te.server.Handler(), "/reports", url.Values{"mode": {"diagram"}, "q": {""}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{`class="mermaid"`, "flowchart LR", "Database", "/static/mermaid.min.js"} {
		if !strings.Contains(body, want) {
			t.Errorf("diagram page missing %q: %q", want, body)
		}
	}
}

func TestReports_DiagramUnknownEntityShowsNote(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/reports", url.Values{"mode": {"diagram"}, "q": {"nothing-here"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "no entity matches") || strings.Contains(body, `class="mermaid"`) {
		t.Errorf("want note and no diagram: %q", body)
	}
}

func TestReports_InvalidModeRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/reports", url.Values{"mode": {"bogus"}, "q": {"x"}})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid mode status = %d, want 400", rr.Code)
	}
}

func TestReports_EmptyQueryRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/reports", url.Values{"mode": {"search"}, "q": {" "}})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty query status = %d, want 400", rr.Code)
	}
}
