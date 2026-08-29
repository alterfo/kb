package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type statusIn struct{}

type statusOut struct {
	CorpusVersion  int    `json:"corpus_version"`
	ChunkCount     int    `json:"chunk_count"`
	EntityCount    int    `json:"entity_count"`
	RelationCount  int    `json:"relation_count"`
	CommunityCount int    `json:"community_count"`
	EmbedModel     string `json:"embed_model"`
	LLMModel       string `json:"llm_model"`
}

func (s *Server) status(ctx context.Context, _ *sdk.CallToolRequest, _ statusIn) (*sdk.CallToolResult, statusOut, error) {
	out := statusOut{EmbedModel: s.deps.EmbedModel, LLMModel: s.deps.LLMModel}

	if s.deps.Versioner != nil {
		if v, err := s.deps.Versioner.CorpusVersion(ctx); err == nil {
			out.CorpusVersion = v
		}
	}
	if s.deps.Vector != nil {
		if chunks, err := s.deps.Vector.AllForBM25(ctx); err == nil {
			out.ChunkCount = len(chunks)
		}
	}
	if s.deps.Graph != nil {
		if entities, err := s.deps.Graph.AllEntities(ctx); err == nil {
			out.EntityCount = len(entities)
		}
		if relations, err := s.deps.Graph.AllRelations(ctx); err == nil {
			out.RelationCount = len(relations)
		}
		if communities, err := s.deps.Graph.AllCommunities(ctx); err == nil {
			out.CommunityCount = len(communities)
		}
	}
	return nil, out, nil
}
