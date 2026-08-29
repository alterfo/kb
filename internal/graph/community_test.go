package graph

import (
	"sort"
	"testing"

	"github.com/alterfo/kb/internal/store/graphstore"
)

// twoCliques builds a deterministic small graph: {a,b,c} densely connected,
// {d,e,f} densely connected, with a single weak bridge a-d, so any sane
// community detector should separate them into two groups.
func twoCliques() ([]graphstore.Entity, []graphstore.Relation) {
	entities := []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
		{ID: "c", Name: "C", Type: "x"},
		{ID: "d", Name: "D", Type: "x"},
		{ID: "e", Name: "E", Type: "x"},
		{ID: "f", Name: "F", Type: "x"},
	}
	rel := func(id, src, dst string, w float64, chunks ...string) graphstore.Relation {
		return graphstore.Relation{ID: id, Src: src, Dst: dst, Type: "rel", Weight: w, SourceChunks: chunks}
	}
	relations := []graphstore.Relation{
		rel("ab", "a", "b", 5, "c1"),
		rel("bc", "b", "c", 5, "c1"),
		rel("ac", "a", "c", 5, "c1"),
		rel("de", "d", "e", 5, "c2"),
		rel("ef", "e", "f", 5, "c2"),
		rel("df", "d", "f", 5, "c2"),
		rel("ad", "a", "d", 0.1, "c3"),
	}
	return entities, relations
}

func TestDetectCommunitiesSeparatesTwoCliques(t *testing.T) {
	entities, relations := twoCliques()

	got := DetectCommunities(entities, relations, 0, 42)
	if len(got) < 2 {
		t.Fatalf("got %d communities, want at least 2: %+v", len(got), got)
	}

	memberOf := map[string]string{}
	for _, c := range got {
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

func TestDetectCommunitiesIsDeterministicForFixedSeed(t *testing.T) {
	entities, relations := twoCliques()

	first := DetectCommunities(entities, relations, 0, 7)
	second := DetectCommunities(entities, relations, 0, 7)

	if len(first) != len(second) {
		t.Fatalf("non-deterministic community count: %d vs %d", len(first), len(second))
	}
	firstIDs := communityIDs(first)
	secondIDs := communityIDs(second)
	if !sameStrings(firstIDs, secondIDs) {
		t.Fatalf("non-deterministic community ids: %v vs %v", firstIDs, secondIDs)
	}
}

func TestDetectCommunitiesNoEdgesGivesSingletons(t *testing.T) {
	entities := []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x"},
		{ID: "b", Name: "B", Type: "x"},
	}
	got := DetectCommunities(entities, nil, 0, 1)
	if len(got) != 2 {
		t.Fatalf("got %d communities, want 2 singletons: %+v", len(got), got)
	}
	for _, c := range got {
		if len(c.Members) != 1 {
			t.Fatalf("community %+v is not a singleton", c)
		}
	}
}

func TestDetectCommunitiesEmptyEntities(t *testing.T) {
	got := DetectCommunities(nil, nil, 0, 1)
	if got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestDetectCommunitiesSourceChunksUnionOfMembers(t *testing.T) {
	entities := []graphstore.Entity{
		{ID: "a", Name: "A", Type: "x", SourceChunks: []string{"c1"}},
		{ID: "b", Name: "B", Type: "x", SourceChunks: []string{"c2"}},
	}
	relations := []graphstore.Relation{
		{ID: "ab", Src: "a", Dst: "b", Type: "rel", Weight: 1, SourceChunks: []string{"c3"}},
	}
	got := DetectCommunities(entities, relations, 0, 1)
	if len(got) != 1 {
		t.Fatalf("got %d communities, want 1", len(got))
	}
	want := []string{"c1", "c2", "c3"}
	sort.Strings(got[0].SourceChunks)
	if !sameStrings(got[0].SourceChunks, want) {
		t.Fatalf("SourceChunks = %v, want %v", got[0].SourceChunks, want)
	}
}

func TestLabelPropagationDeterministicSmallGraph(t *testing.T) {
	entities, relations := twoCliques()
	g, nodeToID, _ := buildWeightedGraph(entities, relations)

	first := labelPropagation(g, nodeToID, 3)
	second := labelPropagation(g, nodeToID, 3)

	if !sameGrouping(first, second) {
		t.Fatalf("labelPropagation not deterministic for fixed seed: %v vs %v", first, second)
	}

	byMember := map[string]int{}
	for gi, group := range first {
		for _, m := range group {
			byMember[m] = gi
		}
	}
	if byMember["a"] != byMember["b"] || byMember["b"] != byMember["c"] {
		t.Fatalf("expected a,b,c grouped together, got %v", first)
	}
	if byMember["d"] != byMember["e"] || byMember["e"] != byMember["f"] {
		t.Fatalf("expected d,e,f grouped together, got %v", first)
	}
}

func communityIDs(cs []graphstore.Community) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameGrouping(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	norm := func(groups [][]string) []string {
		out := make([]string, len(groups))
		for i, g := range groups {
			sorted := append([]string(nil), g...)
			sort.Strings(sorted)
			s := ""
			for _, m := range sorted {
				s += m + ","
			}
			out[i] = s
		}
		sort.Strings(out)
		return out
	}
	return sameStrings(norm(a), norm(b))
}
