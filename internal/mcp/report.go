package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/engine/report"
	"github.com/alterfo/kb/internal/engine/retriever"
)

type generateReportIn struct {
	Mode  string `json:"mode" jsonschema:"search (grounded answer over retrieved chunks), global (GraphRAG community report) or diagram (Mermaid architecture diagram of the knowledge graph)"`
	Query string `json:"query" jsonschema:"the query to report on; for diagram mode an entity name to center on (empty = whole graph)"`
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
	case "diagram":
		if s.deps.Graph == nil {
			return nil, generateReportOut{Report: "no knowledge graph available"}, nil
		}
		res, err := report.Diagram(ctx, s.deps.Graph, s.deps.Chat, s.deps.LLMModel, in.Query, report.DiagramOptions{})
		if err != nil {
			return nil, generateReportOut{}, err
		}
		if res.Mermaid == "" {
			return nil, generateReportOut{Report: res.Reason}, nil
		}
		return nil, generateReportOut{Report: "```mermaid\n" + res.Mermaid + "```"}, nil
	default:
		return nil, generateReportOut{}, fmt.Errorf("mcp: generate_report: unknown mode %q (want search|global|diagram)", in.Mode)
	}
}
