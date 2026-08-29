package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/governance"
)

type trashData struct {
	Entries []governance.TrashEntry
}

func (s *Server) trash() *governance.Trash {
	if s.deps.Governance == nil {
		return nil
	}
	return s.deps.Governance.Trash
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	t := s.trash()
	if t == nil {
		s.render(w, "page-trash", http.StatusOK, page{
			Title:  "Trash",
			Alerts: []Alert{{Kind: "error", Message: "governance is not configured"}},
		})
		return
	}
	entries, err := t.List()
	if err != nil {
		s.render(w, "page-trash", http.StatusOK, page{
			Title:  "Trash",
			Alerts: []Alert{{Kind: "error", Message: "listing trash failed: " + err.Error()}},
		})
		return
	}
	s.render(w, "page-trash", http.StatusOK, page{Title: "Trash", Data: trashData{Entries: entries}})
}

func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.FormValue("path"))
	if path == "" {
		http.Redirect(w, r, "/trash", http.StatusFound)
		return
	}
	if !strings.HasPrefix(path, governance.TrashDirName+"/") {
		path = governance.TrashDirName + "/" + strings.TrimPrefix(path, "/")
	}
	t := s.trash()
	if t == nil {
		http.Redirect(w, r, "/trash", http.StatusFound)
		return
	}
	restored, err := t.Restore(path)
	if err != nil {
		s.alertTrash(w, r, "error", "restore failed: "+err.Error())
		return
	}
	if s.deps.Indexer != nil {
		if err := s.deps.Indexer.AddOrUpdateDocument(r.Context(), restored); err != nil {
			s.alertTrash(w, r, "error", "file restored but re-indexing failed: "+err.Error())
			return
		}
	}
	s.refreshBM25(r.Context())
	s.alertTrash(w, r, "success", "restored "+restored)
}

func (s *Server) alertTrash(w http.ResponseWriter, r *http.Request, kind, msg string) {
	data := trashData{}
	if t := s.trash(); t != nil {
		if entries, err := t.List(); err == nil {
			data.Entries = entries
		}
	}
	s.render(w, "page-trash", http.StatusOK, page{
		Title:  "Trash",
		Alerts: []Alert{{Kind: kind, Message: msg}},
		Data:   data,
	})
}

func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	t := s.trash()
	if t == nil {
		http.Redirect(w, r, "/trash", http.StatusFound)
		return
	}
	n, err := t.Empty()
	msg := "trash emptied (" + strconv.Itoa(n) + " file(s))"
	kind := "success"
	if err != nil {
		msg = "emptying trash failed: " + err.Error()
		kind = "error"
	}
	s.alertTrash(w, r, kind, msg)
}
