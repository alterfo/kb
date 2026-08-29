package got

import (
	"errors"
	"sort"
)

var errDependencyCycle = errors.New("dependency cycle")

// subproblemDAG is the dependency graph between subproblems. Each node is
// identified by a string ID, and each node lists the IDs it depends on.
// Edges point from a node to its dependencies, so a valid topological
// order puts every dependency before its dependent.
type subproblemDAG struct {
	nodes map[string]struct{}
	deps  map[string][]string
}

// newSubproblemDAG builds a DAG from a list of node IDs and an adjacency
// map of node -> dependencies. Self-dependencies, unknown dependency
// targets and duplicate edges are dropped (fail-open): invalid structure
// must not abort the run.
func newSubproblemDAG(ids []string, deps map[string][]string) *subproblemDAG {
	d := &subproblemDAG{
		nodes: make(map[string]struct{}, len(ids)),
		deps:  make(map[string][]string, len(ids)),
	}
	for _, id := range ids {
		d.nodes[id] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := d.nodes[id]; !ok {
			continue
		}
		seen := make(map[string]struct{}, len(deps[id]))
		for _, dep := range deps[id] {
			if dep == id {
				continue
			}
			if _, ok := d.nodes[dep]; !ok {
				continue
			}
			if _, ok := seen[dep]; ok {
				continue
			}
			seen[dep] = struct{}{}
			d.deps[id] = append(d.deps[id], dep)
		}
	}
	return d
}

// topoSort returns the node IDs in a deterministic topological order:
// every dependency precedes its dependent. It errors on a cycle; the caller
// is expected to drop offending edges and retry (fail-open).
func (d *subproblemDAG) topoSort() ([]string, error) {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(d.nodes))
	order := make([]string, 0, len(d.nodes))

	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case visited:
			return nil
		case visiting:
			return errDependencyCycle
		}
		state[id] = visiting
		for _, dep := range d.sortedDeps(id) {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = visited
		order = append(order, id)
		return nil
	}

	for _, id := range d.sortedIDs() {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// levels computes the longest-path level of every node from the sources:
// sources (no dependencies) are level 0, and a node is one level above its
// deepest dependency. It errors on a cycle. Levels power wave scheduling,
// where all nodes of one level may run in parallel once the previous level
// is fully resolved.
func (d *subproblemDAG) levels() (map[string]int, error) {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(d.nodes))
	levels := make(map[string]int, len(d.nodes))

	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case visited:
			return nil
		case visiting:
			return errDependencyCycle
		}
		state[id] = visiting
		level := 0
		for _, dep := range d.sortedDeps(id) {
			if err := visit(dep); err != nil {
				return err
			}
			if depLevel := levels[dep] + 1; depLevel > level {
				level = depLevel
			}
		}
		levels[id] = level
		state[id] = visited
		return nil
	}

	for _, id := range d.sortedIDs() {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return levels, nil
}

func (d *subproblemDAG) sortedIDs() []string {
	ids := make([]string, 0, len(d.nodes))
	for id := range d.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (d *subproblemDAG) sortedDeps(id string) []string {
	deps := append([]string(nil), d.deps[id]...)
	sort.Strings(deps)
	return deps
}

// breakCycles removes dependency edges until the graph is acyclic,
// deterministically dropping the first back edge found by a DFS. It is
// fail-open: a cycle must never abort the run.
func (d *subproblemDAG) breakCycles() {
	for {
		from, to, ok := d.firstCycleEdge()
		if !ok {
			return
		}
		d.removeDep(from, to)
	}
}

// firstCycleEdge returns a dependency edge that participates in a cycle, or
// ok=false when the graph is already acyclic. The result is deterministic:
// nodes and dependencies are walked in sorted order.
func (d *subproblemDAG) firstCycleEdge() (from, to string, ok bool) {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(d.nodes))

	var visit func(string) bool
	visit = func(id string) bool {
		state[id] = visiting
		for _, dep := range d.sortedDeps(id) {
			switch state[dep] {
			case visiting:
				from, to = id, dep
				return true
			case unvisited:
				if visit(dep) {
					return true
				}
			}
		}
		state[id] = visited
		return false
	}

	for _, id := range d.sortedIDs() {
		if state[id] == unvisited {
			if visit(id) {
				return from, to, true
			}
		}
	}
	return "", "", false
}

func (d *subproblemDAG) removeDep(from, to string) {
	deps := d.deps[from]
	for i, dep := range deps {
		if dep == to {
			d.deps[from] = append(deps[:i], deps[i+1:]...)
			return
		}
	}
}
