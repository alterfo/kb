package web

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/alterfo/kb/internal/engine/report"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/store/vector"
)

type reportsData struct {
	Mode   string
	Query  string
	NodeID string
	Report template.HTML
}

func (s *Server) handleReportsForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "page-reports", http.StatusOK, page{
		Title: "Reports",
		Data:  reportsData{Mode: "search"},
	})
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	nodeID := strings.TrimSpace(r.FormValue("node"))
	if nodeID != "" {
		s.handleNodeReport(w, r, nodeID, strings.TrimSpace(r.FormValue("q")))
		return
	}

	mode := r.FormValue("mode")
	q := r.FormValue("q")
	data := reportsData{Mode: mode, Query: q}

	fail := func(msg string) {
		s.render(w, "page-reports", http.StatusBadRequest, page{
			Title:  "Reports",
			Alerts: []Alert{{Kind: "error", Message: msg}},
			Data:   data,
		})
	}
	if strings.TrimSpace(q) == "" {
		fail("query is required")
		return
	}

	ctx := r.Context()
	switch mode {
	case "", "search":
		s.refreshBM25(ctx)
		var (
			chunks []vector.ScoredChunk
			err    error
		)
		if s.retriever != nil {
			chunks, err = s.retriever.Retrieve(ctx, q, retriever.Options{})
		}
		if err != nil {
			fail("retrieval failed: " + err.Error())
			return
		}
		data.Report = renderMarkdown(report.Synthesize(ctx, s.deps.Chat, s.deps.LLMModel, q, chunks))
	case "global":
		if s.deps.Graph == nil {
			fail("knowledge graph is not configured")
			return
		}
		all, err := s.deps.Graph.AllCommunities(ctx)
		if err != nil {
			fail("loading communities failed: " + err.Error())
			return
		}
		data.Report = renderMarkdown(report.GlobalReport(ctx, s.deps.Chat, s.deps.LLMModel, q, all))
	default:
		fail("unknown mode " + mode)
		return
	}
	s.render(w, "page-reports", http.StatusOK, page{Title: "Reports", Data: data})
}
