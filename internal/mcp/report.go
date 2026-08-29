package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/engine/report"
	"github.com/alterfo/kb/internal/engine/retriever"
)

type generateReportIn struct {
	Mode  string `json:"mode" jsonschema:"search (grounded answer over retrieved chunks) or global (GraphRAG community report)"`
	Query string `json:"query" jsonschema:"the query to report on"`
}

type generateReportOut struct {
	Report string `json:"report"`
}

func (s *Server) generateReport(ctx context.Context, _ *sdk.CallToolRequest, in generateReportIn) (*sdk.CallToolResult, generateReportOut, error) {
	switch in.Mode {
	case "", "search":
		s.refreshBM25(ctx)
		chunks, err := s.retriever.Retrieve(ctx, in.Query, retriever.Options{})
		if err != nil {
			return nil, generateReportOut{}, err
		}
		return nil, generateReportOut{Report: report.Synthesize(ctx, s.deps.Chat, s.deps.LLMModel, in.Query, chunks)}, nil
	case "global":
		if s.deps.Graph == nil {
			return nil, generateReportOut{Report: "no knowledge graph available"}, nil
		}
		all, err := s.deps.Graph.AllCommunities(ctx)
		if err != nil {
			return nil, generateReportOut{}, err
		}
		return nil, generateReportOut{Report: report.GlobalReport(ctx, s.deps.Chat, s.deps.LLMModel, in.Query, all)}, nil
	default:
		return nil, generateReportOut{}, fmt.Errorf("mcp: generate_report: unknown mode %q (want search|global)", in.Mode)
	}
}
