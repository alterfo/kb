package graph

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/k8nstantin/go-leiden"

	"github.com/alterfo/kb/internal/store/graphstore"
)

// hierarchicalFixture builds a 18-node graph with a deterministic multi-level
// structure: six 3-node cliques with distinct internal weights, chained by
// bridges with distinct weights. Every edge weight is unique-ish, so no
// quality ties arise and Leiden output is reproducible for a fixed seed.
func hierarchicalFixture() ([]graphstore.Entity, []graphstore.Relation) {
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r"}
	entities := make([]graphstore.Entity, 0, len(names))
	for _, n := range names {
		entities = append(entities, graphstore.Entity{ID: EntityID(n, "x"), Name: n, Type: "x"})
	}

	internalWeights := [][]float64{
		{9, 8, 7},
		{9.5, 8.5, 7.5},
		{10, 9, 8.5},
		{7, 6.5, 6},
		{8.5, 7.5, 6.5},
		{9.2, 8.8, 8.4},
	}
	var relations []graphstore.Relation
	addRel := func(src, dst string, w float64) {
		relations = append(relations, graphstore.Relation{
			ID:     RelationID(EntityID(src, "x"), EntityID(dst, "x"), "links"),
			Src:    EntityID(src, "x"),
			Dst:    EntityID(dst, "x"),
			Type:   "links",
			Weight: w,
		})
	}
	for c := 0; c < 6; c++ {
		i := c * 3
		addRel(names[i], names[i+1], internalWeights[c][0])
		addRel(names[i+1], names[i+2], internalWeights[c][1])
		addRel(names[i], names[i+2], internalWeights[c][2])
	}
	bridges := []struct {
		a, c int
		w    float64
	}{
		{0, 1, 1.0}, {1, 2, 0.9}, {2, 3, 0.8}, {3, 4, 0.7}, {4, 5, 0.6}, {5, 0, 0.5},
	}
	for _, b := range bridges {
		addRel(names[b.a*3+2], names[b.c*3], b.w)
	}
	return entities, relations
}

func TestLeidenDetectorHierarchicalOutput(t *testing.T) {
	entities, relations := hierarchicalFixture()
	idToName := map[string]string{}
	for _, e := range entities {
		idToName[e.ID] = e.Name
	}

	got := LeidenDetector{}.Detect(entities, relations, 7)

	if len(got) == 0 {
		t.Fatalf("no communities produced")
	}
	byLevel := map[int][]graphstore.Community{}
	for _, c := range got {
		byLevel[c.Level] = append(byLevel[c.Level], c)
	}
	var levels []int
	for l := range byLevel {
		levels = append(levels, l)
	}
	sort.Ints(levels)
	if len(levels) != 3 || levels[0] != 0 || levels[1] != 1 || levels[2] != 2 {
		t.Fatalf("expected contiguous levels 0,1,2, got %v", levels)
	}

	assertLevelPartitions := func(l int, want [][]string) {
		t.Helper()
		comms := byLevel[l]
		if len(comms) != len(want) {
			t.Fatalf("level %d: got %d communities, want %d: %+v", l, len(comms), len(want), comms)
		}
		gotSets := make([]string, 0, len(comms))
		for _, c := range comms {
			names := make([]string, 0, len(c.Members))
			for _, m := range c.Members {
				names = append(names, idToName[m])
			}
			sort.Strings(names)
			gotSets = append(gotSets, join(names, ","))
		}
		sort.Strings(gotSets)
		wantSets := make([]string, 0, len(want))
		for _, w := range want {
			sorted := append([]string(nil), w...)
			sort.Strings(sorted)
			wantSets = append(wantSets, join(sorted, ","))
		}
		sort.Strings(wantSets)
		if !equalStrings(gotSets, wantSets) {
			t.Fatalf("level %d communities = %v, want %v", l, gotSets, wantSets)
		}
	}

	assertLevelPartitions(0, [][]string{
		{"a", "b", "c"}, {"d", "e", "f"}, {"g", "h", "i"}, {"j", "k", "l"}, {"m", "n", "o"}, {"p", "q", "r"},
	})
	assertLevelPartitions(1, [][]string{
		{"a", "b", "c"}, {"d", "e", "f"}, {"g", "h", "i", "j", "k", "l"}, {"m", "n", "o", "p", "q", "r"},
	})
	assertLevelPartitions(2, [][]string{
		{"a", "b", "c", "d", "e", "f"}, {"g", "h", "i", "j", "k", "l"}, {"m", "n", "o", "p", "q", "r"},
	})

	for l := range byLevel {
		if !allEntitiesCoveredOnce(byLevel[l], entities) {
			t.Fatalf("level %d does not partition all entities exactly once", l)
		}
	}
}

func TestLeidenDetectorDeterministicForFixedSeed(t *testing.T) {
	entities, relations := hierarchicalFixture()

	first := LeidenDetector{}.Detect(entities, relations, 7)
	second := LeidenDetector{}.Detect(entities, relations, 7)

	if len(first) != len(second) {
		t.Fatalf("community count differs across runs: %d vs %d", len(first), len(second))
	}
	firstIDs := make([]string, len(first))
	secondIDs := make([]string, len(second))
	for i := range first {
		firstIDs[i] = first[i].ID
		secondIDs[i] = second[i].ID
	}
	sort.Strings(firstIDs)
	sort.Strings(secondIDs)
	if !equalStrings(firstIDs, secondIDs) {
		t.Fatalf("community ids differ across runs: %v vs %v", firstIDs, secondIDs)
	}
}

func TestLeidenDetectorFallbackToLouvainOnError(t *testing.T) {
	entities, relations := twoCliques()
	failing := LeidenDetector{
		hierarchical: func(context.Context, int, []leiden.Edge, leiden.Options) (leiden.HierarchicalResult, error) {
			return leiden.HierarchicalResult{}, errors.New("leiden down")
		},
	}

	got := failing.Detect(entities, relations, 42)
	want := LouvainDetector{}.Detect(entities, relations, 42)

	if len(got) != len(want) {
		t.Fatalf("fallback community count = %d, want %d", len(got), len(want))
	}
	gotNames := make([]string, 0, len(got))
	for _, c := range got {
		gotNames = append(gotNames, join(c.Members, ","))
	}
	sort.Strings(gotNames)
	wantNames := make([]string, 0, len(want))
	for _, c := range want {
		wantNames = append(wantNames, join(c.Members, ","))
	}
	sort.Strings(wantNames)
	if !equalStrings(gotNames, wantNames) {
		t.Fatalf("fallback groupings %v differ from Louvain %v", gotNames, wantNames)
	}
}

func TestLeidenDetectorEdgelessSingletons(t *testing.T) {
	entities := []graphstore.Entity{
		{ID: EntityID("a", "x"), Name: "a", Type: "x"},
		{ID: EntityID("b", "x"), Name: "b", Type: "x"},
	}

	got := LeidenDetector{}.Detect(entities, nil, 1)

	if len(got) != 2 {
		t.Fatalf("got %d communities, want 2 singletons: %+v", len(got), got)
	}
	for _, c := range got {
		if c.Level != 0 || len(c.Members) != 1 {
			t.Fatalf("community %+v is not a level-0 singleton", c)
		}
	}
}

func TestLeidenDetectorEmptyEntities(t *testing.T) {
	got := (LeidenDetector{}).Detect(nil, nil, 1)
	if got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestLeidenDetectorSeparatesTwoCliques(t *testing.T) {
	entities, relations := twoCliques()

	got := LeidenDetector{}.Detect(entities, relations, 42)

	byLevel := map[int][]graphstore.Community{}
	for _, c := range got {
		byLevel[c.Level] = append(byLevel[c.Level], c)
	}
	level0, ok := byLevel[0]
	if !ok || len(level0) < 2 {
		t.Fatalf("level 0 has %d communities, want >= 2: %+v", len(level0), got)
	}
	memberOf := map[string]string{}
	for _, c := range level0 {
		for _, m := range c.Members {
			memberOf[m] = c.ID
		}
	}
	if memberOf["a"] != memberOf["b"] || memberOf["b"] != memberOf["c"] {
		t.Fatalf("expected a,b,c in same community, got %+v", memberOf)
	}
	if memberOf["d"] != memberOf["e"] || memberOf["e"] != memberOf["f"] {
		t.Fatalf("expected d,e,f in same community, got %+v", memberOf)
	}
	if memberOf["a"] == memberOf["d"] {
		t.Fatalf("expected the two cliques in different communities, got %+v", memberOf)
	}
}

func TestNewCommunityDetectorSwitch(t *testing.T) {
	if _, ok := NewCommunityDetector("leiden").(LeidenDetector); !ok {
		t.Fatalf("NewCommunityDetector(\"leiden\") = %T, want LeidenDetector", NewCommunityDetector("leiden"))
	}
	if _, ok := NewCommunityDetector(" LEIDEN ").(LeidenDetector); !ok {
		t.Fatalf("NewCommunityDetector(\" LEIDEN \") = %T, want LeidenDetector", NewCommunityDetector(" LEIDEN "))
	}
	for _, algo := range []string{"", "louvain", "bogus"} {
		if _, ok := NewCommunityDetector(algo).(LouvainDetector); !ok {
			t.Fatalf("NewCommunityDetector(%q) = %T, want LouvainDetector", algo, NewCommunityDetector(algo))
		}
	}
}

type goldenGraphJSON struct {
	Entities []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"entities"`
	Relations []struct {
		Src    string  `json:"src"`
		Dst    string  `json:"dst"`
		Type   string  `json:"type"`
		Weight float64 `json:"weight"`
	} `json:"relations"`
}

func TestCommunityDetectorsOnReferenceFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "verify", "testdata", "synthetic", "expected_graph.json"))
	if err != nil {
		t.Fatalf("ReadFile reference fixture: %v", err)
	}
	var golden goldenGraphJSON
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("reference fixture invalid JSON: %v", err)
	}
	entities := make([]graphstore.Entity, 0, len(golden.Entities))
	for _, g := range golden.Entities {
		entities = append(entities, graphstore.Entity{ID: g.ID, Name: g.Name, Type: g.Type})
	}
	relations := make([]graphstore.Relation, 0, len(golden.Relations))
	for _, g := range golden.Relations {
		w := g.Weight
		if w <= 0 {
			w = 1
		}
		relations = append(relations, graphstore.Relation{ID: RelationID(g.Src, g.Dst, g.Type), Src: g.Src, Dst: g.Dst, Type: g.Type, Weight: w})
	}

	louvain := LouvainDetector{}.Detect(entities, relations, 1)
	assertValidPartition(t, louvain, entities, 0)
	for _, c := range louvain {
		if c.Level != 0 {
			t.Fatalf("LouvainDetector produced level %d, want only level 0", c.Level)
		}
	}

	leiden := LeidenDetector{}.Detect(entities, relations, 1)
	if len(leiden) == 0 {
		t.Fatalf("LeidenDetector produced no communities")
	}
	levels := map[int]bool{}
	for _, c := range leiden {
		levels[c.Level] = true
	}
	for l := 0; l < len(levels); l++ {
		if !levels[l] {
			t.Fatalf("Leiden levels not contiguous: %v", levels)
		}
	}
	for l := range levels {
		var levelComms []graphstore.Community
		for _, c := range leiden {
			if c.Level == l {
				levelComms = append(levelComms, c)
			}
		}
		assertValidPartition(t, levelComms, entities, l)
	}
}

func assertValidPartition(t *testing.T, communities []graphstore.Community, entities []graphstore.Entity, level int) {
	t.Helper()
	if len(communities) == 0 {
		t.Fatalf("level %d: no communities", level)
	}
	seen := map[string]int{}
	for _, c := range communities {
		for _, m := range c.Members {
			seen[m]++
		}
	}
	if len(seen) != len(entities) {
		t.Fatalf("level %d: %d entities covered, want %d", level, len(seen), len(entities))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("level %d: entity %q appears %d times", level, id, n)
		}
	}
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func allEntitiesCoveredOnce(communities []graphstore.Community, entities []graphstore.Entity) bool {
	seen := map[string]int{}
	for _, c := range communities {
		for _, m := range c.Members {
			seen[m]++
		}
	}
	if len(seen) != len(entities) {
		return false
	}
	for _, n := range seen {
		if n != 1 {
			return false
		}
	}
	return true
}
