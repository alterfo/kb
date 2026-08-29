package got

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestThoughtGraphSerializesToJSON(t *testing.T) {
	g := ThoughtGraph{
		Query: "q",
		Nodes: []Node{
			{ID: "decompose", Type: NodeDecompose, Status: StatusDone},
			{ID: "subgoal:0", Type: NodeSubgoal, ParentID: "decompose", Status: StatusDone, Coverage: 0.8, Covered: true},
		},
		Refined:     true,
		FinalAnswer: "the answer",
		Sources:     []Source{{FileName: "a.md", FilePath: "notes/a.md", ChunkID: "c1"}},
	}

	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var round ThoughtGraph
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if round.Query != g.Query || round.FinalAnswer != g.FinalAnswer || round.Refined != g.Refined {
		t.Fatalf("round trip mismatch: %+v", round)
	}
	if len(round.Nodes) != 2 || len(round.Sources) != 1 {
		t.Fatalf("round trip mismatch: %+v", round)
	}
	if round.Sources[0].FilePath != "notes/a.md" {
		t.Fatalf("source round trip mismatch: %+v", round.Sources[0])
	}
}

func TestGraphBuilderSetNodeInsertsThenReplaces(t *testing.T) {
	b := newGraphBuilder("q", nil)
	b.setNode(Node{ID: "x", Status: StatusPending})
	b.setNode(Node{ID: "y", Status: StatusPending})
	b.setNode(Node{ID: "x", Status: StatusDone, Answer: "done"})

	snap := b.snapshot()
	if len(snap.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(snap.Nodes))
	}
	var x Node
	for _, n := range snap.Nodes {
		if n.ID == "x" {
			x = n
		}
	}
	if x.Status != StatusDone || x.Answer != "done" {
		t.Fatalf("node x not replaced: %+v", x)
	}
}

func TestGraphBuilderSetStageUpdatesOnlyStage(t *testing.T) {
	b := newGraphBuilder("q", nil)
	b.setNode(Node{ID: "x", Status: StatusRunning, Query: "orig"})
	b.setStage("x", StageSynthesizing)

	snap := b.snapshot()
	if snap.Nodes[0].Stage != StageSynthesizing || snap.Nodes[0].Query != "orig" || snap.Nodes[0].Status != StatusRunning {
		t.Fatalf("unexpected node: %+v", snap.Nodes[0])
	}
}

func TestGraphBuilderProgressCallback(t *testing.T) {
	var snapshots []ThoughtGraph
	b := newGraphBuilder("q", func(g ThoughtGraph) {
		snapshots = append(snapshots, g)
	})

	b.setNode(Node{ID: "x", Status: StatusPending})
	b.setStage("x", StageRetrieving)
	b.setFinal(false, "answer", nil)

	if len(snapshots) != 3 {
		t.Fatalf("got %d progress calls, want 3", len(snapshots))
	}
	if snapshots[2].FinalAnswer != "answer" {
		t.Fatalf("last snapshot missing final answer: %+v", snapshots[2])
	}
}

func TestGraphBuilderSnapshotIsIndependentCopy(t *testing.T) {
	b := newGraphBuilder("q", nil)
	b.setNode(Node{ID: "x", Status: StatusPending})

	snap := b.snapshot()
	snap.Nodes[0].Status = "mutated"

	fresh := b.snapshot()
	if fresh.Nodes[0].Status != StatusPending {
		t.Fatalf("mutating a snapshot leaked into the builder: %+v", fresh.Nodes[0])
	}
}

func TestDedupSourcesByFilePathSorted(t *testing.T) {
	in := []Source{
		{FileName: "b.md", FilePath: "notes/b.md"},
		{FileName: "a.md", FilePath: "notes/a.md"},
		{FileName: "b.md", FilePath: "notes/b.md"},
		{FileName: "no-path.md", FilePath: ""},
	}
	out := dedupSources(in)
	if len(out) != 3 {
		t.Fatalf("got %d sources, want 3: %+v", len(out), out)
	}
	if out[0].FilePath != "" || out[1].FilePath != "notes/a.md" || out[2].FilePath != "notes/b.md" {
		t.Fatalf("unexpected order: %+v", out)
	}
}

func TestNodeDepsJSONRoundTrip(t *testing.T) {
	n := Node{ID: "subgoal:1", Type: NodeSubgoal, Query: "find B", Status: StatusDone, Deps: []string{"0"}}
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"deps":["0"]`) {
		t.Fatalf("marshaled node missing deps: %s", data)
	}

	var round Node
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(round.Deps) != 1 || round.Deps[0] != "0" {
		t.Fatalf("round trip deps mismatch: %+v", round.Deps)
	}
}

func TestThoughtGraphLegacyRecordCompat(t *testing.T) {
	legacy := `{"query":"q","nodes":[{"id":"decompose","type":"decompose","status":"done"},{"id":"subgoal:0","type":"subgoal","parent_id":"decompose","query":"find A","status":"done","answer":"answer"}],"refined":false,"final_answer":"answer"}`

	var g ThoughtGraph
	if err := json.Unmarshal([]byte(legacy), &g); err != nil {
		t.Fatalf("Unmarshal legacy record: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(g.Nodes))
	}
	if g.Nodes[1].Deps != nil {
		t.Fatalf("legacy subgoal node gained deps: %+v", g.Nodes[1].Deps)
	}

	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), `"deps"`) {
		t.Fatalf("legacy round trip must stay additive (no deps key): %s", data)
	}
}

func TestPlanNodeRecordsTopoOrder(t *testing.T) {
	cfg := baseConfig()
	cfg.Retriever = &recordingRetriever{}
	cfg.Chat = dependencyAwareChat{}
	g := New(cfg).Run(context.Background(), "q")

	var plan *Node
	for i := range g.Nodes {
		if g.Nodes[i].Type == NodePlan {
			plan = &g.Nodes[i]
			break
		}
	}
	if plan == nil {
		t.Fatalf("missing plan node in graph nodes: %+v", g.Nodes)
	}
	if plan.ID != NodePlan || plan.ParentID != NodeDecompose || plan.Status != StatusDone {
		t.Fatalf("unexpected plan node: %+v", *plan)
	}
	if want := "find A -> find B -> find C"; plan.Query != want {
		t.Fatalf("plan query = %q, want %q", plan.Query, want)
	}
}
