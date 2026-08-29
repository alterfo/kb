package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/governance"
)

type cleanupResultView struct {
	OK     bool
	Action string
	Detail string
}

type cleanupProposalView struct {
	Kind         string
	Primary      string
	Paths        []string
	Content      string
	OriginalSize int
	NewSize      int
}

type cleanupData struct {
	Note       string
	Duplicates []governance.DuplicateGroup
	Empty      []governance.DocRecord
	Merges     []governance.MergeGroup
	Compress   []governance.DocRecord
	Results    []cleanupResultView
	Proposals  []cleanupProposalView
}

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	data := s.cleanupView()
	s.render(w, "page-cleanup", http.StatusOK, page{Title: "Cleanup", Data: data})
}

func (s *Server) cleanupView() cleanupData {
	data := cleanupData{}
	if s.deps.Governance == nil {
		data.Note = "governance is not configured"
		return data
	}
	plan, err := governance.Scan(s.deps.Root, governance.DefaultScanOptions())
	if err != nil {
		data.Note = "scan failed: " + err.Error()
		return data
	}
	data.Duplicates = plan.Duplicates
	data.Empty = plan.Empty
	data.Merges = plan.Merge
	data.Compress = plan.Compress
	return data
}

func (s *Server) handleCleanupApply(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "page-cleanup", http.StatusBadRequest, page{
			Title:  "Cleanup",
			Alerts: []Alert{{Kind: "error", Message: "invalid form: " + err.Error()}},
			Data:   cleanupData{},
		})
		return
	}
	data := s.cleanupView()
	if s.deps.Governance == nil {
		data.Note = "governance is not configured"
		s.render(w, "page-cleanup", http.StatusOK, page{Title: "Cleanup", Data: data})
		return
	}
	actions := r.Form["action"]
	if len(actions) == 0 {
		data.Note = "no actions selected"
		s.render(w, "page-cleanup", http.StatusOK, page{Title: "Cleanup", Data: data})
		return
	}
	results := s.deps.Governance.Apply(r.Context(), actions)
	for _, res := range results {
		view := cleanupResultView{OK: res.OK, Action: res.Action, Detail: res.Detail}
		if res.Proposal != nil {
			data.Proposals = append(data.Proposals, cleanupProposalView{
				Kind:         res.Proposal.Kind,
				Primary:      res.Proposal.Primary,
				Paths:        res.Proposal.Paths,
				Content:      res.Proposal.Content,
				OriginalSize: res.Proposal.OriginalSize,
				NewSize:      res.Proposal.NewSize,
			})
			continue
		}
		data.Results = append(data.Results, view)
	}
	if len(actions) == len(data.Results)+len(data.Proposals) {
		data.Note = "applied " + strconv.Itoa(len(data.Results)) + " action(s)"
	}
	s.refreshBM25(r.Context())
	s.render(w, "page-cleanup", http.StatusOK, page{Title: "Cleanup", Data: data})
}

func (s *Server) handleCleanupRewrite(w http.ResponseWriter, r *http.Request) {
	if s.deps.Governance == nil {
		http.Redirect(w, r, "/cleanup", http.StatusFound)
		return
	}
	paths := strings.Split(r.FormValue("paths"), ";")
	content := r.FormValue("content")
	detail, err := s.deps.Governance.ApplyRewrite(r.Context(), paths, content)
	alert := Alert{Kind: "success", Message: detail}
	if err != nil {
		alert = Alert{Kind: "error", Message: "rewrite failed: " + err.Error()}
	}
	s.refreshBM25(r.Context())
	data := s.cleanupView()
	data.Note = alert.Message
	s.render(w, "page-cleanup", http.StatusOK, page{Title: "Cleanup", Data: data})
}
