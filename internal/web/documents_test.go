package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/store/graphstore"
)

func TestDocuments_ListAndFilter(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/n1.md", doc("n1", "notes", "body one"))
	writeDoc(t, te.root, "github/g1.md", doc("g1", "github", "body two"))

	rr := getPage(t, te.server.Handler(), "/documents")
	body := rr.Body.String()
	for _, want := range []string{"notes/n1.md", "github/g1.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("documents list missing %q", want)
		}
	}

	rr = getPage(t, te.server.Handler(), "/documents?source=notes")
	body = rr.Body.String()
	if strings.Contains(body, "github/g1.md") {
		t.Errorf("filter leaked other source")
	}
	if !strings.Contains(body, "notes/n1.md") {
		t.Errorf("filter dropped notes doc")
	}
}

func TestListDocuments_ExtractsSummary(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/summary.md", connector.Document{
		ID:      "sum",
		Source:  "notes",
		Title:   "Summarized",
		Body:    "Full body.",
		Summary: "Short summary.",
	})
	writeDoc(t, te.root, "notes/plain.md", connector.Document{
		ID:     "plain",
		Source: "notes",
		Title:  "Plain",
		Body:   "No summary.",
	})

	entries, err := te.server.scanDocuments()
	if err != nil {
		t.Fatalf("scanDocuments: %v", err)
	}
	byPath := map[string]docEntry{}
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	if got := byPath["notes/summary.md"].Summary; got != "Short summary." {
		t.Fatalf("summary = %q, want Short summary.", got)
	}
	if got := byPath["notes/plain.md"].Summary; got != "" {
		t.Fatalf("plain summary = %q, want empty", got)
	}
}

func TestDocumentView_RendersMarkdownAndEscapesHTML(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/view.md", connector.Document{
		ID: "view", Source: "notes", Title: "View Title",
		Body: "# Heading\n\n<script>alert(1)</script>\n\n**bold**",
	})

	rr := getPage(t, te.server.Handler(), "/documents/view?path=notes/view.md")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"View Title", "<h1", "<strong>bold</strong>"} {
		if !strings.Contains(body, want) {
			t.Errorf("view page missing %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Errorf("raw HTML leaked into rendered document")
	}
}

func TestDocumentView_TraversalRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/documents/view?path="+url.QueryEscape("../secret"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("traversal status = %d, want 400", rr.Code)
	}
}

func TestDocumentView_NotFound(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/documents/view?path=notes/missing.md")
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing status = %d, want 404", rr.Code)
	}
}

func TestDocumentView_NotIndexedShowsMessage(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/unindexed.md", doc("unindexed", "notes", "not indexed yet"))

	rr := getPage(t, te.server.Handler(), "/documents/view?path=notes/unindexed.md")
	body := rr.Body.String()
	if !strings.Contains(body, "not been indexed into the knowledge graph yet") {
		t.Errorf("expected not-indexed message, got %q", body)
	}
}

func TestDocumentView_IndexedNoEntitiesShowsMessage(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/noentities.md", doc("noentities", "notes", "indexed but no graph entities"))
	te.index(t, "notes/noentities.md")

	rr := getPage(t, te.server.Handler(), "/documents/view?path=notes/noentities.md")
	body := rr.Body.String()
	if !strings.Contains(body, "found no entities in this document") {
		t.Errorf("expected no-entities message, got %q", body)
	}
}

func TestDocumentView_ShowsRelatedEntitiesAndRelations(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/related.md", doc("related", "notes", "Alice works with Bob on the roadmap."))
	te.index(t, "notes/related.md")

	ctx := context.Background()
	chunks, err := te.vector.ChunksByDoc(ctx, engine.DocRefID("notes/related.md"))
	if err != nil || len(chunks) == 0 {
		t.Fatalf("ChunksByDoc: %v (chunks=%d)", err, len(chunks))
	}
	chunkID := chunks[0].ID

	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "ent-alice", Name: "Alice", Type: "person", SourceChunks: []string{chunkID}},
		{ID: "ent-bob", Name: "Bob", Type: "person", SourceChunks: []string{chunkID}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "rel-alice-bob", Src: "ent-alice", Dst: "ent-bob", Type: "WORKS_WITH", SourceChunks: []string{chunkID}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/documents/view?path=notes/related.md")
	body := rr.Body.String()
	if !strings.Contains(body, "Alice") || !strings.Contains(body, "Bob") {
		t.Errorf("expected related entities Alice and Bob, got %q", body)
	}
	if !strings.Contains(body, "WORKS_WITH") {
		t.Errorf("expected relation type WORKS_WITH, got %q", body)
	}
	if !strings.Contains(body, "/graph/entity?id=ent-alice") {
		t.Errorf("expected link to entity panel, got %q", body)
	}
}

func TestAdd_WritesAndIndexes(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/add", url.Values{
		"path":    {"notes/hello.md"},
		"title":   {"Hello"},
		"content": {"hello world from add route"},
	})
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	loc, err := url.PathUnescape(rr.Header().Get("Location"))
	if err != nil {
		t.Fatalf("unescaping redirect location: %v", err)
	}
	if !strings.Contains(loc, "/documents/view?path=notes/hello.md") {
		t.Errorf("redirect location = %q", rr.Header().Get("Location"))
	}
	if _, err := os.Stat(filepath.Join(te.root, "notes", "hello.md")); err != nil {
		t.Errorf("note file not written: %v", err)
	}

	rr = getPage(t, te.server.Handler(), "/search?q=hello+world+from+add")
	if !strings.Contains(rr.Body.String(), "notes/hello.md") {
		t.Errorf("added note not searchable")
	}
}

func TestAdd_TraversalRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/add", url.Values{
		"path":    {"../evil.md"},
		"content": {"x"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("traversal status = %d, want 400", rr.Code)
	}
}

func TestAdd_EmptyContentRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := postForm(t, te.server.Handler(), "/add", url.Values{
		"path":    {"notes/x.md"},
		"content": {"   "},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty content status = %d, want 400", rr.Code)
	}
}

type failingGraphStore struct {
	graphstore.Store
}

func (failingGraphStore) AllEntities(ctx context.Context) ([]graphstore.Entity, error) {
	return nil, errors.New("injected graph failure")
}

func TestDocumentView_GraphReadFailureShowsNotIndexed(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/graphfail.md", doc("graphfail", "notes", "indexed but graph read fails"))
	te.index(t, "notes/graphfail.md")
	te.server.deps.Graph = failingGraphStore{Store: te.graph}

	rr := getPage(t, te.server.Handler(), "/documents/view?path=notes/graphfail.md")
	if !strings.Contains(rr.Body.String(), "not been indexed into the knowledge graph yet") {
		t.Errorf("expected not-indexed message on graph read failure, got %q", rr.Body.String())
	}
}

func TestDocumentView_RelationFilterUsesSourceChunks(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/relchunks.md", doc("relchunks", "notes", "Alice works with Bob."))
	te.index(t, "notes/relchunks.md")

	ctx := context.Background()
	chunks, err := te.vector.ChunksByDoc(ctx, engine.DocRefID("notes/relchunks.md"))
	if err != nil || len(chunks) == 0 {
		t.Fatalf("ChunksByDoc: %v (chunks=%d)", err, len(chunks))
	}
	chunkID := chunks[0].ID

	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "ent-alice", Name: "Alice", Type: "person", SourceChunks: []string{chunkID}},
		{ID: "ent-bob", Name: "Bob", Type: "person", SourceChunks: []string{chunkID}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "rel-manual", Src: "ent-alice", Dst: "ent-bob", Type: "MANUAL", SourceChunks: []string{"manual:rel-manual"}},
		{ID: "rel-extracted", Src: "ent-alice", Dst: "ent-bob", Type: "WORKS_WITH", SourceChunks: []string{chunkID}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/documents/view?path=notes/relchunks.md")
	body := rr.Body.String()
	if !strings.Contains(body, "WORKS_WITH") {
		t.Errorf("expected relation backed by document chunk, got %q", body)
	}
	if strings.Contains(body, "MANUAL") {
		t.Errorf("relation not backed by document chunk should not appear: %q", body)
	}
}
