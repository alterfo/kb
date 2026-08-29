package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type reindexIn struct {
	Path string `json:"path,omitempty" jsonschema:"KB_ROOT-relative file or directory to reindex; empty reindexes the whole tree"`
}

type reindexOut struct {
	Indexed int `json:"indexed"`
	Skipped int `json:"skipped"`
	Removed int `json:"removed"`
}

func (s *Server) reindex(ctx context.Context, _ *sdk.CallToolRequest, in reindexIn) (*sdk.CallToolResult, reindexOut, error) {
	if s.deps.Indexer == nil {
		return nil, reindexOut{}, fmt.Errorf("mcp: reindex: no indexer configured")
	}
	res, err := s.deps.Indexer.Reindex(ctx, in.Path)
	if err != nil {
		return nil, reindexOut{}, err
	}
	s.refreshBM25(ctx)
	return nil, reindexOut{Indexed: res.Indexed, Skipped: res.Skipped, Removed: res.Removed}, nil
}
