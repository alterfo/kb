package web

import (
	"context"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/report"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/store/vector"
)

// searchHistoryLimit bounds how many recent searches are shown alongside
// results; the full log is unbounded in search_history, this is just the
// display window.
const searchHistoryLimit = 10

type searchResult struct {
	FilePath   string
	FileName   string
	Source     string
	Score      float64
	ChunkIndex int
	Text       string
}

type searchHistoryView struct {
	ID           int64
	Query        string
	ResultsCount int
	DurationMS   int64
	CreatedAt    string
}

type savedSearchView struct {
	Query        string
	SourceFilter string
	Answer       template.HTML
	ResultsCount int
	DurationMS   int64
	CreatedAt    string
}

type searchData struct {
	Query                   string
	Source                  string
	Path                    string
	Answer                  template.HTML
	SynthesisFallback       bool
	SynthesisFallbackReason string
	Saved                   *savedSearchView
	Results                 []searchResult
	History                 []searchHistoryView
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query().Get("q")
	source := r.URL.Query().Get("source")
	data := searchData{Query: q, Source: source, History: s.recentSearches(ctx)}

	if idParam := r.URL.Query().Get("id"); idParam != "" {
		if id, err := strconv.ParseInt(idParam, 10, 64); err == nil && s.renderSavedSearch(ctx, w, r, id, data) {
			return
		}
	}

	if path := r.URL.Query().Get("path"); path != "" {
		s.searchByPath(ctx, w, r, path, data)
		return
	}

	if q == "" {
		s.renderSearch(w, r, http.StatusOK, nil, data)
		return
	}

	s.refreshBM25(ctx)
	filter := vector.Filter{}
	if source != "" {
		filter.Sources = s.expandSources(source)
	}
	start := s.deps.Now()
	var (
		chunks []vector.ScoredChunk
		err    error
	)
	if s.retriever != nil {
		chunks, err = s.retriever.Retrieve(ctx, q, retriever.Options{Filter: filter})
	}
	if err != nil {
		s.renderSearch(w, r, http.StatusOK, []Alert{{Kind: "error", Message: "search failed: " + err.Error()}}, data)
		return
	}
	for _, c := range chunks {
		data.Results = append(data.Results, searchResult{
			FilePath:   c.FilePath,
			FileName:   c.FileName,
			Source:     c.Source,
			Score:      c.Score,
			ChunkIndex: c.ChunkIndex,
			Text:       c.Text,
		})
	}
	answer, fallback, reason := report.SynthesizeResult(ctx, s.deps.Chat, s.deps.LLMModel, q, chunks)
	stored := answer
	if fallback {
		data.SynthesisFallback = true
		data.SynthesisFallbackReason = reason
		stored = ""
	} else {
		data.Answer = renderMarkdown(answer)
	}
	s.recordSearch(ctx, q, source, len(data.Results), stored, s.deps.Now().Sub(start))
	data.History = s.recentSearches(ctx)
	s.renderSearch(w, r, http.StatusOK, nil, data)
}

// renderSavedSearch shows a stored search snapshot without re-running
// retrieval or writing another history row. It returns false when the id is
// unknown or unreadable so the caller can fall back to a normal search.
func (s *Server) renderSavedSearch(ctx context.Context, w http.ResponseWriter, r *http.Request, id int64, data searchData) bool {
	if s.deps.History == nil {
		return false
	}
	entry, ok, err := s.deps.History.SearchEntryByID(ctx, id)
	if err != nil || !ok {
		return false
	}
	data.Query = entry.Query
	data.Source = entry.SourceFilter
	data.Saved = &savedSearchView{
		Query:        entry.Query,
		SourceFilter: entry.SourceFilter,
		Answer:       renderMarkdown(entry.Answer),
		ResultsCount: entry.ResultsCount,
		DurationMS:   entry.DurationMS,
		CreatedAt:    entry.CreatedAt.Format(time.RFC3339),
	}
	s.renderSearch(w, r, http.StatusOK, nil, data)
	return true
}

// renderSearch picks the fragment template for htmx-driven searches (so a
// submit only swaps the results, not the whole page) and the full page
// otherwise, so /search stays a bookmarkable/shareable URL either way.
func (s *Server) renderSearch(w http.ResponseWriter, r *http.Request, status int, alerts []Alert, data searchData) {
	if isHtmx(r) {
		s.render(w, "search-results", status, page{Title: "Search", Alerts: alerts, Data: data})
		return
	}
	s.render(w, "page-search", status, page{Title: "Search", Alerts: alerts, Data: data})
}

// recordSearch is fail-open: history is diagnostic, not load-bearing, so a
// write failure must never surface as a search error to the user.
func (s *Server) recordSearch(ctx context.Context, query, source string, resultsCount int, answer string, duration time.Duration) {
	if s.deps.History == nil {
		return
	}
	_ = s.deps.History.RecordSearch(ctx, query, source, resultsCount, answer, duration, s.deps.Now())
}

// recentSearches is fail-open: a history read failure just means no recent
// searches are shown, not a broken search page.
func (s *Server) recentSearches(ctx context.Context) []searchHistoryView {
	if s.deps.History == nil {
		return nil
	}
	entries, err := s.deps.History.SearchHistory(ctx, searchHistoryLimit)
	if err != nil {
		return nil
	}
	views := make([]searchHistoryView, 0, len(entries))
	for _, e := range entries {
		views = append(views, searchHistoryView{
			ID:           e.ID,
			Query:        e.Query,
			ResultsCount: e.ResultsCount,
			DurationMS:   e.DurationMS,
			CreatedAt:    e.CreatedAt.Format(time.RFC3339),
		})
	}
	return views
}

// expandSources resolves a virtual collection name to the concrete source
// names it covers; unknown selectors and missing/unreadable sources.yaml
// fall back to the selector itself (fail-open).
func (s *Server) expandSources(selector string) []string {
	cfg, err := config.LoadSourcesFile(s.deps.SourcesPath)
	if err != nil {
		return []string{selector}
	}
	return cfg.SourceNamesFor(selector)
}

func (s *Server) searchByPath(ctx context.Context, w http.ResponseWriter, r *http.Request, path string, data searchData) {
	abs, err := resolveWithin(s.deps.Root, path)
	if err != nil {
		s.renderSearch(w, r, http.StatusBadRequest, []Alert{{Kind: "error", Message: err.Error()}}, data)
		return
	}
	if _, err := os.Stat(abs); err != nil {
		s.renderSearch(w, r, http.StatusNotFound, []Alert{{Kind: "error", Message: "document not found: " + path}}, data)
		return
	}
	data.Path = path
	if s.deps.Vector != nil {
		if chunks, err := s.deps.Vector.AllForBM25(ctx); err == nil {
			rel := filepath.ToSlash(path)
			for _, c := range chunks {
				if filepath.ToSlash(c.FilePath) != rel {
					continue
				}
				data.Results = append(data.Results, searchResult{
					FilePath:   c.FilePath,
					FileName:   c.FileName,
					Source:     c.Source,
					Score:      1,
					ChunkIndex: c.ChunkIndex,
					Text:       c.Text,
				})
			}
		}
	}
	sort.Slice(data.Results, func(i, j int) bool {
		if data.Results[i].ChunkIndex != data.Results[j].ChunkIndex {
			return data.Results[i].ChunkIndex < data.Results[j].ChunkIndex
		}
		return data.Results[i].Text < data.Results[j].Text
	})
	s.renderSearch(w, r, http.StatusOK, nil, data)
}
