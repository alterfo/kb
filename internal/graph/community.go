package graph

import (
	"math/rand/v2"
	"sort"

	"gonum.org/v1/gonum/graph/community"
	"gonum.org/v1/gonum/graph/simple"

	"github.com/alterfo/kb/internal/store/graphstore"
)

// DetectCommunities groups entities into communities via Louvain modularity
// optimization (gonum's community.Modularize), falling back to a
// deterministic seeded label-propagation pass if Modularize panics (it is
// documented as unstable on degenerate/disconnected inputs). Entities with
// no edges among them each form a singleton community.
func DetectCommunities(entities []graphstore.Entity, relations []graphstore.Relation, level int, seed int64) []graphstore.Community {
	if len(entities) == 0 {
		return nil
	}

	g, nodeToID, edgeCount := buildWeightedGraph(entities, relations)

	var groups [][]string
	if edgeCount == 0 {
		ids := make([]string, 0, len(nodeToID))
		for _, id := range nodeToID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			groups = append(groups, []string{id})
		}
	} else {
		groups = modularize(g, nodeToID, seed)
	}

	out := make([]graphstore.Community, 0, len(groups))
	for _, members := range groups {
		sort.Strings(members)
		out = append(out, graphstore.Community{
			ID:           CommunityID(level, members, relations),
			Level:        level,
			Members:      members,
			SourceChunks: chunkUnionForMembers(members, entities, relations),
		})
	}
	return out
}

// buildWeightedGraph builds a gonum weighted undirected graph from entities
// and relations, assigning deterministic node ids by sorting entity ids.
// Parallel relations between the same pair of entities have their weights
// summed into a single edge.
func buildWeightedGraph(entities []graphstore.Entity, relations []graphstore.Relation) (*simple.WeightedUndirectedGraph, map[int64]string, int) {
	ids := make([]string, 0, len(entities))
	for _, e := range entities {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)

	idToNode := make(map[string]int64, len(ids))
	nodeToID := make(map[int64]string, len(ids))
	for i, id := range ids {
		idToNode[id] = int64(i)
		nodeToID[int64(i)] = id
	}

	g := simple.NewWeightedUndirectedGraph(0, 0)
	for _, id := range ids {
		g.AddNode(simple.Node(idToNode[id]))
	}

	edgeCount := 0
	for _, r := range relations {
		u, ok1 := idToNode[r.Src]
		v, ok2 := idToNode[r.Dst]
		if !ok1 || !ok2 || u == v {
			continue
		}
		w := r.Weight
		if w <= 0 {
			w = 1
		}
		if existing := g.WeightedEdge(u, v); existing != nil {
			w += existing.Weight()
		}
		g.SetWeightedEdge(simple.WeightedEdge{F: simple.Node(u), T: simple.Node(v), W: w})
		edgeCount++
	}
	return g, nodeToID, edgeCount
}

func modularize(g *simple.WeightedUndirectedGraph, nodeToID map[int64]string, seed int64) (groups [][]string) {
	defer func() {
		if recover() != nil {
			groups = labelPropagation(g, nodeToID, seed)
		}
	}()

	reduced := community.Modularize(g, 1, rand.NewPCG(uint64(seed), uint64(seed)))
	for _, group := range reduced.Communities() {
		var members []string
		for _, n := range group {
			members = append(members, nodeToID[n.ID()])
		}
		groups = append(groups, members)
	}
	return groups
}

// labelPropagation is the deterministic fallback community-detection
// algorithm: each node repeatedly adopts the majority (weighted) label
// among its neighbors, ties broken by smallest label id for reproducibility
// under a fixed seed.
func labelPropagation(g *simple.WeightedUndirectedGraph, nodeToID map[int64]string, seed int64) [][]string {
	var nodeIDs []int64
	for id := range nodeToID {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })

	label := make(map[int64]int64, len(nodeIDs))
	for _, id := range nodeIDs {
		label[id] = id
	}

	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)))
	const maxRounds = 20
	for round := 0; round < maxRounds; round++ {
		order := append([]int64(nil), nodeIDs...)
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

		changed := false
		for _, n := range order {
			weight := map[int64]float64{}
			from := g.From(n)
			for from.Next() {
				neighbor := from.Node().ID()
				w, _ := g.Weight(n, neighbor)
				weight[label[neighbor]] += w
			}
			if len(weight) == 0 {
				continue
			}
			best := label[n]
			bestWeight := -1.0
			var bestLabels []int64
			for l, w := range weight {
				if w > bestWeight {
					bestWeight = w
					bestLabels = []int64{l}
				} else if w == bestWeight {
					bestLabels = append(bestLabels, l)
				}
			}
			sort.Slice(bestLabels, func(i, j int) bool { return bestLabels[i] < bestLabels[j] })
			if bestLabels[0] != best {
				label[n] = bestLabels[0]
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	byLabel := map[int64][]string{}
	for _, id := range nodeIDs {
		byLabel[label[id]] = append(byLabel[label[id]], nodeToID[id])
	}
	groups := make([][]string, 0, len(byLabel))
	for _, members := range byLabel {
		groups = append(groups, members)
	}
	return groups
}

func chunkUnionForMembers(members []string, entities []graphstore.Entity, relations []graphstore.Relation) []string {
	memberSet := make(map[string]struct{}, len(members))
	for _, m := range members {
		memberSet[m] = struct{}{}
	}

	seen := map[string]struct{}{}
	out := []string{}
	add := func(chunks []string) {
		for _, c := range chunks {
			if _, ok := seen[c]; !ok {
				seen[c] = struct{}{}
				out = append(out, c)
			}
		}
	}
	for _, e := range entities {
		if _, ok := memberSet[e.ID]; ok {
			add(e.SourceChunks)
		}
	}
	for _, r := range relations {
		_, sOK := memberSet[r.Src]
		_, dOK := memberSet[r.Dst]
		if sOK && dOK {
			add(r.SourceChunks)
		}
	}
	sort.Strings(out)
	return out
}
