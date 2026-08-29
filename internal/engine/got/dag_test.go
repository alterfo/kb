package got

import (
	"reflect"
	"testing"
)

func TestTopoSortLinearChain(t *testing.T) {
	d := newSubproblemDAG([]string{"a", "b", "c", "d"}, map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"d"},
	})
	got, err := d.topoSort()
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	want := []string{"d", "c", "b", "a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTopoSortDiamond(t *testing.T) {
	d := newSubproblemDAG([]string{"a", "b", "c", "d"}, map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
	})
	got, err := d.topoSort()
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	assertTopoOrder(t, d, got)
}

func TestTopoSortDisconnectedComponents(t *testing.T) {
	d := newSubproblemDAG([]string{"a", "b", "c", "d"}, map[string][]string{
		"a": {"b"},
		"c": {"d"},
	})
	got, err := d.topoSort()
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	assertTopoOrder(t, d, got)
}

func TestTopoSortCycleReturnsError(t *testing.T) {
	d := newSubproblemDAG([]string{"a", "b"}, map[string][]string{
		"a": {"b"},
		"b": {"a"},
	})
	got, err := d.topoSort()
	if err == nil {
		t.Fatalf("expected cycle error, got order %v", got)
	}
}

func TestTopoSortSingleNode(t *testing.T) {
	d := newSubproblemDAG([]string{"a"}, nil)
	got, err := d.topoSort()
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("got %v, want [a]", got)
	}
}

func TestTopoSortEmptyGraph(t *testing.T) {
	d := newSubproblemDAG(nil, nil)
	got, err := d.topoSort()
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestTopoSortStableOrdering(t *testing.T) {
	d := newSubproblemDAG([]string{"c", "a", "d", "b"}, map[string][]string{
		"a": {"b", "d"},
		"c": {"a"},
	})
	first, err := d.topoSort()
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	for i := 0; i < 5; i++ {
		next, err := d.topoSort()
		if err != nil {
			t.Fatalf("topoSort: %v", err)
		}
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("unstable ordering: %v then %v", first, next)
		}
	}
	assertTopoOrder(t, d, first)
}

func TestLevelsLinearChain(t *testing.T) {
	d := newSubproblemDAG([]string{"a", "b", "c"}, map[string][]string{
		"a": {"b"},
		"b": {"c"},
	})
	got, err := d.levels()
	if err != nil {
		t.Fatalf("levels: %v", err)
	}
	want := map[string]int{"a": 2, "b": 1, "c": 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLevelsDiamond(t *testing.T) {
	d := newSubproblemDAG([]string{"a", "b", "c", "d"}, map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
	})
	got, err := d.levels()
	if err != nil {
		t.Fatalf("levels: %v", err)
	}
	want := map[string]int{"a": 2, "b": 1, "c": 1, "d": 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLevelsDisconnectedSources(t *testing.T) {
	d := newSubproblemDAG([]string{"a", "b"}, nil)
	got, err := d.levels()
	if err != nil {
		t.Fatalf("levels: %v", err)
	}
	want := map[string]int{"a": 0, "b": 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLevelsCycleReturnsError(t *testing.T) {
	d := newSubproblemDAG([]string{"a", "b"}, map[string][]string{
		"a": {"b"},
		"b": {"a"},
	})
	got, err := d.levels()
	if err == nil {
		t.Fatalf("expected cycle error, got levels %v", got)
	}
}

func TestNewSubproblemDAGDropsSelfAndUnknownDeps(t *testing.T) {
	d := newSubproblemDAG([]string{"a", "b"}, map[string][]string{
		"a": {"a", "b", "missing", "b"},
	})
	if len(d.deps["a"]) != 1 || d.deps["a"][0] != "b" {
		t.Fatalf("deps not normalized: %v", d.deps["a"])
	}
}

func assertTopoOrder(t *testing.T, d *subproblemDAG, order []string) {
	t.Helper()
	if len(order) != len(d.nodes) {
		t.Fatalf("got %d nodes, want %d (%v)", len(order), len(d.nodes), order)
	}
	pos := make(map[string]int, len(order))
	for i, id := range order {
		if _, ok := d.nodes[id]; !ok {
			t.Fatalf("unknown node in order: %q", id)
		}
		pos[id] = i
	}
	for id, deps := range d.deps {
		for _, dep := range deps {
			if pos[dep] >= pos[id] {
				t.Fatalf("dep %q of %q not before it in %v", dep, id, order)
			}
		}
	}
}
