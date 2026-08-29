package web

import (
	"context"
	"net/http"
)

type homeData struct {
	Chunks             int
	Documents          int
	Entities           int
	Relations          int
	Communities        int
	Sources            int
	CorpusVersion      int
	EmbedModel         string
	LLMModel           string
	StaleSources       int
	VirtualCollections int
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := homeData{
		EmbedModel: s.deps.EmbedModel,
		LLMModel:   s.deps.LLMModel,
	}

	if s.deps.Versioner != nil {
		if v, err := s.deps.Versioner.CorpusVersion(ctx); err == nil {
			data.CorpusVersion = v
		}
	}
	if s.deps.Vector != nil {
		if chunks, err := s.deps.Vector.AllForBM25(ctx); err == nil {
			data.Chunks = len(chunks)
			seen := map[string]bool{}
			for _, c := range chunks {
				seen[c.RefDocID] = true
			}
			data.Documents = len(seen)
		}
	}
	if s.deps.Graph != nil {
		if entities, err := s.deps.Graph.AllEntities(ctx); err == nil {
			data.Entities = len(entities)
		}
		if relations, err := s.deps.Graph.AllRelations(ctx); err == nil {
			data.Relations = len(relations)
		}
		if communities, err := s.deps.Graph.AllCommunities(ctx); err == nil {
			data.Communities = len(communities)
		}
	}
	if cfg, err := s.loadSources(ctx); err == nil {
		data.Sources = len(cfg.Sources)
		data.VirtualCollections = len(cfg.VirtualCollections)
	}
	data.StaleSources = s.staleSourceCount(ctx)

	s.render(w, "page-home", http.StatusOK, page{Title: "Overview", Data: data})
}

func (s *Server) staleSourceCount(ctx context.Context) int {
	rows := s.integrationRows(ctx)
	n := 0
	for _, row := range rows {
		if row.Stale {
			n++
		}
	}
	return n
}
