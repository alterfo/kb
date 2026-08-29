package retriever

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

// GraphStore is the subset of graphstore.Store the retriever needs for
// graph-aware retrieval. Satisfied by *sqlite.GraphStore.
type GraphStore interface {
	MatchEntities(ctx context.Context, names []string, at ...time.Time) ([]graphstore.Entity, error)
	Neighbors(ctx context.Context, entityID string, hops int, at ...time.Time) ([]graphstore.Entity, []graphstore.Relation, error)
	CommunitiesFor(ctx context.Context, ids []string) ([]graphstore.Community, error)
	AllCommunities(ctx context.Context) ([]graphstore.Community, error)
	StaleCommunityCount(ctx context.Context) (int, error)
	RefreshStaleCommunities(ctx context.Context) (int, error)
}

const (
	maxEntityNgram      = 4
	maxNeighborEntities = 15
	maxCommunities      = 3
)

var queryTokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// graphRankLists produces up to two additional rank lists for RRF fusion:
// graph-neighbor chunks and community-summary "chunks". Fail-open: a nil
// Graph, no linked entities, or any store error yields no lists, leaving
// the caller's pure hybrid result untouched.
func (r *Retriever) graphRankLists(ctx context.Context, query string, filter vector.Filter, chunkByID map[string]vector.Chunk) [][]string {
	if r.cfg.Graph == nil {
		return nil
	}

	linked := linkEntities(ctx, r.cfg.Graph, query)
	if len(linked) == 0 {
		return nil
	}

	var lists [][]string
	if list := r.neighborChunkList(ctx, linked, filter, chunkByID); len(list) > 0 {
		lists = append(lists, list)
	}
	if list := r.communityChunkList(ctx, linked, chunkByID); len(list) > 0 {
		lists = append(lists, list)
	}
	return lists
}

func LinkEntities(ctx context.Context, gs GraphStore, query string) []graphstore.Entity {
	return linkEntities(ctx, gs, query)
}

// linkEntities resolves candidate entity names embedded in query (as
// contiguous word n-grams) against the graph, fail-open to nil.
func linkEntities(ctx context.Context, gs GraphStore, query string) []graphstore.Entity {
	candidates := queryNgrams(query, maxEntityNgram)
	if len(candidates) == 0 {
		return nil
	}
	entities, err := gs.MatchEntities(ctx, candidates)
	if err != nil {
		return nil
	}
	return entities
}

// queryNgrams returns every distinct contiguous word n-gram of query, for
// n in [1, maxN], as a fuzzy stand-in for exact entity-name matching (a
// multi-word entity name embedded anywhere in the query still matches).
// Windows of two or more tokens are emitted both space-joined and
// dot-joined, so Go symbol names (methods "Type.Method", package-qualified
// "pkg.Func") link even though the tokenizer treats '.' as a separator.
func queryNgrams(query string, maxN int) []string {
	tokens := queryTokenRe.FindAllString(query, -1)
	seen := make(map[string]struct{})
	var out []string
	for n := 1; n <= maxN && n <= len(tokens); n++ {
		for i := 0; i+n <= len(tokens); i++ {
			window := tokens[i : i+n]
			for _, sep := range []string{" ", "."} {
				gram := strings.Join(window, sep)
				key := strings.ToLower(gram)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, gram)
			}
		}
	}
	return out
}

// neighborChunkList expands linked entities by 1-2 hops, scores each
// neighbor by the total weight of relations connecting it back to a linked
// entity, and returns its source chunks ordered by that score (highest
// first), capped to the retriever's candidate window budget. Requires BM25
// as the corpus-wide id->chunk resolver; fail-open to nil without it.
func (r *Retriever) neighborChunkList(ctx context.Context, linked []graphstore.Entity, filter vector.Filter, chunkByID map[string]vector.Chunk) []string {
	if r.cfg.BM25 == nil {
		return nil
	}

	linkedIDs := make(map[string]struct{}, len(linked))
	for _, e := range linked {
		linkedIDs[e.ID] = struct{}{}
	}

	neighborScore := make(map[string]float64)
	neighborsByID := make(map[string]graphstore.Entity)
	for _, e := range linked {
		neighbors, relations, err := r.cfg.Graph.Neighbors(ctx, e.ID, r.cfg.GraphHops)
		if err != nil {
			continue
		}
		for _, n := range neighbors {
			neighborsByID[n.ID] = n
		}
		for _, rel := range relations {
			neighborScore[rel.Src] += rel.Weight
			neighborScore[rel.Dst] += rel.Weight
		}
	}

	type scoredEntity struct {
		id    string
		score float64
	}
	ranked := make([]scoredEntity, 0, len(neighborsByID))
	for id := range neighborsByID {
		if _, ok := linkedIDs[id]; ok {
			continue
		}
		ranked = append(ranked, scoredEntity{id: id, score: neighborScore[id]})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].id < ranked[j].id
	})
	if len(ranked) > maxNeighborEntities {
		ranked = ranked[:maxNeighborEntities]
	}

	var ids []string
	seen := make(map[string]struct{})
	for _, re := range ranked {
		for _, cid := range neighborsByID[re.id].SourceChunks {
			if _, ok := seen[cid]; ok {
				continue
			}
			chunk, ok := r.cfg.BM25.Chunk(cid)
			if !ok {
				continue
			}
			if !filter.Matches(chunk.Source, chunk.Metadata) {
				continue
			}
			seen[cid] = struct{}{}
			chunkByID[cid] = chunk
			ids = append(ids, cid)
			if len(ids) >= r.cfg.CandidateK {
				return ids
			}
		}
	}
	return ids
}

// communityChunkList finds communities containing any linked entity, ranks
// them by how many linked entities they contain, and injects each summary
// as a synthetic chunk (id "community:<id>", its own doc id so the per-doc
// cap treats each community independently).
func (r *Retriever) communityChunkList(ctx context.Context, linked []graphstore.Entity, chunkByID map[string]vector.Chunk) []string {
	linkedIDs := make([]string, 0, len(linked))
	linkedSet := make(map[string]struct{}, len(linked))
	for _, e := range linked {
		linkedIDs = append(linkedIDs, e.ID)
		linkedSet[e.ID] = struct{}{}
	}

	communities, err := r.cfg.Graph.CommunitiesFor(ctx, linkedIDs)
	if err != nil || len(communities) == 0 {
		return nil
	}

	type scoredCommunity struct {
		c    graphstore.Community
		hits int
	}
	scored := make([]scoredCommunity, 0, len(communities))
	for _, c := range communities {
		hits := 0
		for _, m := range c.Members {
			if _, ok := linkedSet[m]; ok {
				hits++
			}
		}
		scored = append(scored, scoredCommunity{c: c, hits: hits})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].hits != scored[j].hits {
			return scored[i].hits > scored[j].hits
		}
		return scored[i].c.ID < scored[j].c.ID
	})
	if len(scored) > maxCommunities {
		scored = scored[:maxCommunities]
	}

	var ids []string
	for _, sc := range scored {
		if strings.TrimSpace(sc.c.Summary) == "" {
			continue
		}
		chunk := communityChunk(sc.c)
		chunkByID[chunk.ID] = chunk
		ids = append(ids, chunk.ID)
	}
	return ids
}

// communityChunk wraps a community summary as a synthetic chunk (id
// "community:<id>", its own doc id so the per-doc cap treats each community
// independently).
func communityChunk(c graphstore.Community) vector.Chunk {
	id := "community:" + c.ID
	return vector.Chunk{
		ID:       id,
		RefDocID: id,
		Text:     c.Summary,
		FilePath: "graph/communities/" + c.ID,
		FileName: c.Title,
		Source:   "graph",
	}
}
