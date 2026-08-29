package web

import (
	"encoding/json"
	"net/http"

	"github.com/alterfo/kb/internal/connector"
)

// handleAPIIngest implements the write side of the APISink binding
// (APISink -> server -> Indexer): it indexes a single document without
// touching the filesystem.
func (s *Server) handleAPIIngest(w http.ResponseWriter, r *http.Request) {
	if s.deps.Indexer == nil {
		http.Error(w, "no indexer configured", http.StatusServiceUnavailable)
		return
	}
	var d connector.Document
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		http.Error(w, "invalid document: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.deps.Indexer.IndexDocument(r.Context(), d); err != nil {
		http.Error(w, "index: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.refreshBM25(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

type apiPruneRequest struct {
	Source   string   `json:"source"`
	Seen     []string `json:"seen"`
	Prefixes []string `json:"prefixes,omitempty"`
}

// handleAPIPrune removes every indexed document of source whose id is not
// in seen, mirroring FileSink.Prune for the API-fed path.
func (s *Server) handleAPIPrune(w http.ResponseWriter, r *http.Request) {
	if s.deps.Indexer == nil {
		http.Error(w, "no indexer configured", http.StatusServiceUnavailable)
		return
	}
	var p apiPruneRequest
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	seen := make(map[string]struct{}, len(p.Seen))
	for _, id := range p.Seen {
		seen[id] = struct{}{}
	}
	if _, err := s.deps.Indexer.PruneSource(r.Context(), p.Source, seen, p.Prefixes...); err != nil {
		http.Error(w, "prune: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.refreshBM25(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

type apiTombstoneRequest struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

// handleAPITombstone removes the indexed document for (source, id),
// mirroring FileSink.Tombstone for the API-fed path.
func (s *Server) handleAPITombstone(w http.ResponseWriter, r *http.Request) {
	if s.deps.Indexer == nil {
		http.Error(w, "no indexer configured", http.StatusServiceUnavailable)
		return
	}
	var p apiTombstoneRequest
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.deps.Indexer.RemoveDocumentBySourceID(r.Context(), p.Source, p.ID); err != nil {
		http.Error(w, "tombstone: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.refreshBM25(r.Context())
	w.WriteHeader(http.StatusNoContent)
}
