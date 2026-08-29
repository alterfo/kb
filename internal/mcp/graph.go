package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/store/graphstore"
)

type graphQueryIn struct {
	Entity string `json:"entity" jsonschema:"entity name to look up"`
	Hops   int    `json:"hops,omitempty" jsonschema:"neighbor expansion depth (default 1)"`
}

type graphQueryOut struct {
	Matched     []graphstore.Entity    `json:"matched"`
	Neighbors   []graphstore.Entity    `json:"neighbors,omitempty"`
	Relations   []graphstore.Relation  `json:"relations,omitempty"`
	Communities []graphstore.Community `json:"communities,omitempty"`
}

func (s *Server) graphQuery(ctx context.Context, _ *sdk.CallToolRequest, in graphQueryIn) (*sdk.CallToolResult, graphQueryOut, error) {
	if s.deps.Graph == nil {
		return nil, graphQueryOut{}, nil
	}

	hops := in.Hops
	if hops <= 0 {
		hops = 1
	}

	matched, err := s.deps.Graph.MatchEntities(ctx, []string{in.Entity})
	if err != nil || len(matched) == 0 {
		return nil, graphQueryOut{}, err
	}

	out := graphQueryOut{Matched: matched}
	seenNeighbors := map[string]bool{}
	seenRelations := map[string]bool{}
	var memberIDs []string
	for _, m := range matched {
		memberIDs = append(memberIDs, m.ID)
		neighbors, relations, err := s.deps.Graph.Neighbors(ctx, m.ID, hops)
		if err != nil {
			continue
		}
		for _, n := range neighbors {
			if !seenNeighbors[n.ID] {
				seenNeighbors[n.ID] = true
				out.Neighbors = append(out.Neighbors, n)
			}
		}
		for _, r := range relations {
			if !seenRelations[r.ID] {
				seenRelations[r.ID] = true
				out.Relations = append(out.Relations, r)
			}
		}
	}

	communities, err := s.deps.Graph.CommunitiesFor(ctx, memberIDs)
	if err == nil {
		out.Communities = communities
	}
	return nil, out, nil
}
