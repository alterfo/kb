package graph

import (
	"context"
	"sort"
	"strings"

	"github.com/k8nstantin/go-leiden"

	"github.com/alterfo/kb/internal/store/graphstore"
)

type CommunityDetector interface {
	Detect(entities []graphstore.Entity, relations []graphstore.Relation, seed int64) []graphstore.Community
}

type LouvainDetector struct{}

func (LouvainDetector) Detect(entities []graphstore.Entity, relations []graphstore.Relation, seed int64) []graphstore.Community {
	return DetectCommunities(entities, relations, 0, seed)
}

type LeidenDetector struct {
	hierarchical func(ctx context.Context, nNodes int, edges []leiden.Edge, opts leiden.Options) (leiden.HierarchicalResult, error)
}

func (d LeidenDetector) Detect(entities []graphstore.Entity, relations []graphstore.Relation, seed int64) []graphstore.Community {
	if len(entities) == 0 {
		return nil
	}
	run := d.hierarchical
	if run == nil {
		run = leiden.HierarchicalLeiden
	}
	n, nodeToID, _, edges := buildLeidenGraph(entities, relations)
	if len(edges) == 0 {
		return DetectCommunities(entities, relations, 0, seed)
	}
	opts := leiden.DefaultOptions()
	opts.Seed = seed
	res, err := run(context.Background(), n, edges, opts)
	if err != nil {
		return LouvainDetector{}.Detect(entities, relations, seed)
	}
	return communitiesFromHierarchy(res.Levels, nodeToID, entities, relations)
}

func NewCommunityDetector(algo string) CommunityDetector {
	if strings.EqualFold(strings.TrimSpace(algo), "leiden") {
		return LeidenDetector{}
	}
	return LouvainDetector{}
}

func buildLeidenGraph(entities []graphstore.Entity, relations []graphstore.Relation) (int, map[int64]string, map[string]int64, []leiden.Edge) {
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

	weight := map[[2]int64]float64{}
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
		key := [2]int64{u, v}
		weight[key] += w
	}
	edges := make([]leiden.Edge, 0, len(weight))
	for key, w := range weight {
		edges = append(edges, leiden.Edge{From: int(key[0]), To: int(key[1]), Weight: w})
	}
	return len(ids), nodeToID, idToNode, edges
}

func communitiesFromHierarchy(levels []leiden.LevelResult, nodeToID map[int64]string, entities []graphstore.Entity, relations []graphstore.Relation) []graphstore.Community {
	var communities []graphstore.Community
	var prev [][]string
	level := 0
	for _, lv := range levels {
		groups := groupingByCluster(lv.Partition, nodeToID)
		if sameGroupings(groups, prev) {
			continue
		}
		prev = groups
		for _, members := range groups {
			sort.Strings(members)
			communities = append(communities, graphstore.Community{
				ID:           CommunityID(level, members, relations),
				Level:        level,
				Members:      members,
				SourceChunks: chunkUnionForMembers(members, entities, relations),
			})
		}
		level++
	}
	return communities
}

func groupingByCluster(partition []int, nodeToID map[int64]string) [][]string {
	clusters := map[int][]int{}
	for node, cl := range partition {
		clusters[cl] = append(clusters[cl], node)
	}
	var clusterIDs []int
	for cl := range clusters {
		clusterIDs = append(clusterIDs, cl)
	}
	sort.Ints(clusterIDs)
	out := make([][]string, 0, len(clusterIDs))
	for _, cl := range clusterIDs {
		members := make([]string, 0, len(clusters[cl]))
		for _, n := range clusters[cl] {
			members = append(members, nodeToID[int64(n)])
		}
		sort.Strings(members)
		out = append(out, members)
	}
	return out
}

func sameGroupings(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	norm := func(groups [][]string) string {
		var parts []string
		for _, g := range groups {
			parts = append(parts, strings.Join(g, ","))
		}
		sort.Strings(parts)
		return strings.Join(parts, ";")
	}
	return norm(a) == norm(b)
}
