package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/metrics"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/store/vector"
)

type searchIn struct {
	Query   string   `json:"query" jsonschema:"the search query"`
	K       int      `json:"k,omitempty" jsonschema:"max results to return (default 10)"`
	Source  string   `json:"source,omitempty" jsonschema:"restrict results to a single virtual collection (e.g. notes, github)"`
	Sources []string `json:"sources,omitempty" jsonschema:"restrict results to one of several virtual collections"`
}

type searchResult struct {
	FilePath string  `json:"file_path"`
	FileName string  `json:"file_name"`
	Source   string  `json:"source"`
	Score    float64 `json:"score"`
	Text     string  `json:"text"`
}

type searchOut struct {
	Results         []searchResult `json:"results"`
	Metrics         metrics.Values `json:"metrics"`
	Degraded        []string       `json:"degraded,omitempty"`
	ContractVersion int            `json:"contract_version"`
}

func (s *Server) search(ctx context.Context, _ *sdk.CallToolRequest, in searchIn) (*sdk.CallToolResult, searchOut, error) {
	s.refreshBM25(ctx)

	filter := vector.Filter{}
	switch {
	case in.Source != "":
		filter.Sources = s.expandSources(in.Source)
	case len(in.Sources) > 0:
		for _, src := range in.Sources {
			filter.Sources = append(filter.Sources, s.expandSources(src)...)
		}
	}

	result := s.retriever.RetrieveWithResult(ctx, in.Query, retriever.Options{K: in.K, Filter: filter})
	return nil, searchOut{
		Results:         toSearchResults(result.Chunks),
		Metrics:         result.Metrics,
		Degraded:        result.Degraded,
		ContractVersion: ResponseContractVersion,
	}, nil
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

func toSearchResults(chunks []vector.ScoredChunk) []searchResult {
	out := make([]searchResult, len(chunks))
	for i, c := range chunks {
		out[i] = searchResult{
			FilePath: c.FilePath,
			FileName: c.FileName,
			Source:   c.Source,
			Score:    c.Score,
			Text:     c.Text,
		}
	}
	return out
}
