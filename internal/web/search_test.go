package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/llm"
)

func doc(id, source, body string) connector.Document {
	return connector.Document{ID: id, Source: source, Title: id, Body: body}
}

func TestSearch_ReturnsIndexedResults(t *testing.T) {
	te := newTestEnv(t, &fakeChat{resp: llm.ChatResponse{Content: "grounded answer (rain.md)"}})
	writeDoc(t, te.root, "notes/rain.md", doc("rain", "notes", "The rain in Spain falls mainly on the plain."))
	te.index(t, "notes/rain.md")

	rr := getPage(t, te.server.Handler(), "/search?q=rain+Spain")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "notes/rain.md") {
		t.Errorf("search page missing result path: %q", body)
	}
	if !strings.Contains(body, "Synthesis") {
		t.Errorf("search page missing synthesis section")
	}
}

func TestSearch_SynthesisFallbackRendersBannerNotAnswer(t *testing.T) {
	te := newTestEnv(t, &fakeChat{err: errors.New("boom")})
	writeDoc(t, te.root, "notes/rain.md", doc("rain", "notes", "The rain in Spain falls mainly on the plain."))
	te.index(t, "notes/rain.md")

	rr := getPage(t, te.server.Handler(), "/search?q=rain+Spain")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Синтез недоступен") {
		t.Errorf("fallback should render a warning banner: %q", body)
	}
	if strings.Contains(body, "relevant sources found but synthesis unavailable") {
		t.Errorf("fallback text must not render as an answer: %q", body)
	}
	if !strings.Contains(body, "notes/rain.md") {
		t.Errorf("sources list should remain after synthesis fallback: %q", body)
	}
}

func TestSearch_EmptyCorpus(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/search?q=anything")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "no results") {
		t.Errorf("expected no-results state, got %q", body)
	}
}

func TestSearch_SourceFilter(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/a.md", doc("a", "notes", "unique-search-token"))
	writeDoc(t, te.root, "github/b.md", doc("b", "github", "unique-search-token"))
	te.index(t, "notes/a.md")
	te.index(t, "github/b.md")

	rr := getPage(t, te.server.Handler(), "/search?q=unique-search-token&source=notes")
	body := rr.Body.String()
	if strings.Contains(body, "github/b.md") {
		t.Errorf("source filter leaked github result: %q", body)
	}
	if !strings.Contains(body, "notes/a.md") {
		t.Errorf("source filter dropped notes result: %q", body)
	}
}

func TestSearch_ByPathReturnsChunks(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/pathdoc.md", doc("pathdoc", "notes", "chunk-one token chunk-two token chunk-three token"))
	te.index(t, "notes/pathdoc.md")

	rr := getPage(t, te.server.Handler(), "/search?path=notes/pathdoc.md")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "chunk-one") {
		t.Errorf("by-path search missing chunk text")
	}
}

func TestSearch_ByPathTraversalRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/search?path="+url.QueryEscape("../etc/passwd"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("traversal status = %d, want 400", rr.Code)
	}
}

func TestSearch_ByPathNotFound(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/search?path=notes/missing.md")
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing path status = %d, want 404", rr.Code)
	}
}

func TestSearch_FiltersByVirtualCollection(t *testing.T) {
	te := newTestEnv(t, nil)
	sourcesYAML := "sources:\n  - name: tg\n    type: telegram\n  - name: notes\n    type: file\nvirtual_collections:\n  chats: [telegram:*]\n"
	if err := os.WriteFile(filepath.Join(te.root, "sources.yaml"), []byte(sourcesYAML), 0o644); err != nil {
		t.Fatalf("writing sources.yaml: %v", err)
	}
	writeDoc(t, te.root, "notes/rain.md", doc("rain", "notes", "The rain in Spain falls mainly on the plain."))
	writeDoc(t, te.root, "telegram/tg-chat.md", doc("tg-chat", "tg", "zebra tokens live in the chat feed."))
	te.index(t, "notes/rain.md")
	te.index(t, "telegram/tg-chat.md")

	rr := getPage(t, te.server.Handler(), "/search?q=zebra&source=chats")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "telegram/tg-chat.md") {
		t.Errorf("virtual-collection search missing chat result: %q", body)
	}
	if strings.Contains(body, "notes/rain.md") {
		t.Errorf("virtual-collection search leaked a non-collection source: %q", body)
	}
}

func TestSearch_RecordsHistory(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/rain.md", doc("rain", "notes", "The rain in Spain falls mainly on the plain."))
	te.index(t, "notes/rain.md")

	getPage(t, te.server.Handler(), "/search?q=rain+Spain")

	entries, err := te.history.SearchHistory(context.Background(), 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("SearchHistory: got %d entries, want 1", len(entries))
	}
	if entries[0].Query != "rain Spain" {
		t.Errorf("recorded query = %q, want %q", entries[0].Query, "rain Spain")
	}
	if entries[0].ResultsCount != 1 {
		t.Errorf("recorded results_count = %d, want 1", entries[0].ResultsCount)
	}
}

func TestSearch_EmptyQueryDoesNotRecordHistory(t *testing.T) {
	te := newTestEnv(t, nil)
	getPage(t, te.server.Handler(), "/search")

	entries, err := te.history.SearchHistory(context.Background(), 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("SearchHistory: got %d entries for empty query, want 0", len(entries))
	}
}

func TestSearch_ShowsRecentSearches(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/rain.md", doc("rain", "notes", "unique-history-token"))
	te.index(t, "notes/rain.md")

	getPage(t, te.server.Handler(), "/search?q=unique-history-token")
	rr := getPage(t, te.server.Handler(), "/search")
	body := rr.Body.String()
	if !strings.Contains(body, "Recent searches") {
		t.Errorf("expected recent searches section, got %q", body)
	}
	if !strings.Contains(body, "unique-history-token") {
		t.Errorf("expected recent search entry in history, got %q", body)
	}
}

func TestSearch_HtmxRequestReturnsFragmentOnly(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/rain.md", doc("rain", "notes", "The rain in Spain falls mainly on the plain."))
	te.index(t, "notes/rain.md")

	req := httptest.NewRequest(http.MethodGet, "/search?q=rain+Spain", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	te.server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("htmx fragment should not include full page shell: %q", body)
	}
	if !strings.Contains(body, "notes/rain.md") {
		t.Errorf("htmx fragment missing result: %q", body)
	}
}

func TestSearch_SavedHistoryRendersWithoutRetrieval(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/rain.md", doc("rain", "notes", "The rain in Spain falls mainly on the plain."))
	te.index(t, "notes/rain.md")

	now := te.server.deps.Now()
	if err := te.history.RecordSearch(context.Background(), "saved query", "notes", 1, "saved answer text", 42*time.Millisecond, now); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	entries, err := te.history.SearchHistory(context.Background(), 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("SearchHistory: got %d entries, want 1", len(entries))
	}
	id := entries[0].ID

	rr := getPage(t, te.server.Handler(), "/search?id="+strconv.FormatInt(id, 10))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Saved search") {
		t.Errorf("saved view missing section: %q", body)
	}
	if !strings.Contains(body, "saved answer text") {
		t.Errorf("saved view missing stored answer: %q", body)
	}
	if strings.Contains(body, "notes/rain.md") {
		t.Errorf("saved view re-ran retrieval and leaked result path: %q", body)
	}

	entries, err = te.history.SearchHistory(context.Background(), 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("viewing saved history wrote a new row: got %d entries, want 1", len(entries))
	}
}

func TestSearch_SavedHistoryEmptyAnswerShowsMetadataAndReRun(t *testing.T) {
	te := newTestEnv(t, nil)
	now := te.server.deps.Now()
	if err := te.history.RecordSearch(context.Background(), "empty answer", "wiki", 2, "", 50*time.Millisecond, now); err != nil {
		t.Fatalf("RecordSearch: %v", err)
	}
	entries, err := te.history.SearchHistory(context.Background(), 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("SearchHistory: got %d entries, want 1", len(entries))
	}
	id := entries[0].ID

	rr := getPage(t, te.server.Handler(), "/search?id="+strconv.FormatInt(id, 10))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Saved search") {
		t.Errorf("saved view missing section: %q", body)
	}
	if !strings.Contains(body, "Повторить поиск") {
		t.Errorf("saved view missing re-run button: %q", body)
	}
	if !strings.Contains(body, "2 results") {
		t.Errorf("saved view missing results count: %q", body)
	}
	if strings.Contains(body, "<h3>Synthesis</h3>") {
		t.Errorf("empty answer should not render a synthesis section: %q", body)
	}
}

func TestSearch_HistoryLinkUsesID(t *testing.T) {
	te := newTestEnv(t, nil)
	writeDoc(t, te.root, "notes/rain.md", doc("rain", "notes", "unique-history-token"))
	te.index(t, "notes/rain.md")

	getPage(t, te.server.Handler(), "/search?q=unique-history-token")
	entries, err := te.history.SearchHistory(context.Background(), 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("SearchHistory: got %d entries, want 1", len(entries))
	}
	want := "/search?id=" + strconv.FormatInt(entries[0].ID, 10)

	rr := getPage(t, te.server.Handler(), "/search")
	body := rr.Body.String()
	if !strings.Contains(body, want) {
		t.Errorf("history link = %q, want it to contain %q", body, want)
	}
	if strings.Contains(body, "/search?q=unique-history-token") {
		t.Errorf("history link should not re-run by query: %q", body)
	}
}

func TestSearch_UnknownHistoryIDFallsBackToSearch(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/search?id=999999")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "Saved search") {
		t.Errorf("unknown id should fall back to search, got saved view: %q", body)
	}
	if !strings.Contains(body, "no results") {
		t.Errorf("unknown id fallback missing normal search page: %q", body)
	}
}
