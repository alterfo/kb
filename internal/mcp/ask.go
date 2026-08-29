package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/engine/got"
)

type askIn struct {
	Query string `json:"query" jsonschema:"the question to answer"`
}

type askOut struct {
	Answer  string       `json:"answer"`
	Refined bool         `json:"refined"`
	Sources []got.Source `json:"sources"`
}

func (s *Server) ask(ctx context.Context, _ *sdk.CallToolRequest, in askIn) (*sdk.CallToolResult, askOut, error) {
	s.refreshBM25(ctx)

	graph := s.orch.Run(ctx, in.Query)
	return nil, askOut{
		Answer:  graph.FinalAnswer,
		Refined: graph.Refined,
		Sources: graph.Sources,
	}, nil
}
