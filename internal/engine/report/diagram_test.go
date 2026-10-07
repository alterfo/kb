package report

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
)

type fakeDiagramGraph struct {
	entities  []graphstore.Entity
	relations []graphstore.Relation
	err       error
}

func (f fakeDiagramGraph) MatchEntities(_ context.Context, names []string, _ ...time.Time) ([]graphstore.Entity, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []graphstore.Entity
	for _, e := range f.entities {
		for _, n := range names {
			if strings.EqualFold(e.Name, n) {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

func (f fakeDiagramGraph) Neighbors(_ context.Context, id string, _ int, _ ...time.Time) ([]graphstore.Entity, []graphstore.Relation, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	near := map[string]bool{id: true}
	var rels []graphstore.Relation
	for _, r := range f.relations {
		if r.Src == id || r.Dst == id {
			rels = append(rels, r)
			near[r.Src], near[r.Dst] = true, true
		}
	}
	var ents []graphstore.Entity
	for _, e := range f.entities {
		if near[e.ID] && e.ID != id {
			ents = append(ents, e)
		}
	}
	return ents, rels, nil
}

func (f fakeDiagramGraph) AllEntities(context.Context) ([]graphstore.Entity, error) {
	return f.entities, f.err
}

func (f fakeDiagramGraph) AllRelations(context.Context) ([]graphstore.Relation, error) {
	return f.relations, f.err
}

func sampleDiagramGraph() fakeDiagramGraph {
	return fakeDiagramGraph{
		entities: []graphstore.Entity{
			{ID: "ent:web", Name: "Web", Type: "component", Degree: 3},
			{ID: "ent:retriever", Name: "Retriever", Type: "component", Degree: 2},
			{ID: "ent:sqlite", Name: "SQLite", Type: "storage", Degree: 2},
			{ID: "ent:llm", Name: "LLM", Type: "service", Degree: 1},
			{ID: "ent:lonely", Name: "Lonely", Type: "other", Degree: 0},
		},
		relations: []graphstore.Relation{
			{ID: "r1", Src: "ent:web", Dst: "ent:retriever", Type: "calls"},
			{ID: "r2", Src: "ent:retriever", Dst: "ent:sqlite", Type: "reads"},
			{ID: "r3", Src: "ent:web", Dst: "ent:llm", Type: "calls"},
		},
	}
}

func TestDiagramRendersValidatedPlan(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: "```json\n" + `{"nodes":[` +
		`{"id":"e1","label":"Dashboard","group":"Serving"},` +
		`{"id":"e2","label":"Search","group":"Serving"},` +
		`{"id":"e3","label":"Store","group":"Data"}],` +
		`"edges":[{"from":"e1","to":"e2","label":"queries"},{"from":"e2","to":"e3","label":"reads"}]}` + "\n```"}}

	res, err := Diagram(context.Background(), sampleDiagramGraph(), chat, "m", "", DiagramOptions{})
	if err != nil {
		t.Fatalf("Diagram: %v", err)
	}
	if res.Fallback {
		t.Fatalf("unexpected fallback: %s", res.Reason)
	}
	if res.Nodes != 3 || res.Edges != 2 {
		t.Fatalf("nodes=%d edges=%d, want 3/2", res.Nodes, res.Edges)
	}
	for _, want := range []string{"flowchart LR", `subgraph g1["Serving"]`, `["Dashboard"]`, `|"queries"|`, `["Store"]`} {
		if !strings.Contains(res.Mermaid, want) {
			t.Fatalf("mermaid missing %q:\n%s", want, res.Mermaid)
		}
	}
	if strings.Contains(res.Mermaid, `subgraph g2["Data"]`) {
		t.Fatalf("single-node group must not become a subgraph:\n%s", res.Mermaid)
	}
}

func TestDiagramDropsHallucinatedNodesAndEdges(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: `{"nodes":[` +
		`{"id":"e1","label":"Web"},{"id":"e2","label":"Retriever"},{"id":"e99","label":"Ghost"}],` +
		`"edges":[{"from":"e1","to":"e2","label":"calls"},{"from":"e2","to":"e1","label":"back"},` +
		`{"from":"e1","to":"e99","label":"x"},{"from":"e1","to":"e5","label":"made up"}]}`}}

	res, err := Diagram(context.Background(), sampleDiagramGraph(), chat, "m", "", DiagramOptions{})
	if err != nil {
		t.Fatalf("Diagram: %v", err)
	}
	if res.Fallback {
		t.Fatalf("unexpected fallback: %s", res.Reason)
	}
	if strings.Contains(res.Mermaid, "Ghost") {
		t.Fatalf("hallucinated node leaked:\n%s", res.Mermaid)
	}
	if res.Nodes != 2 || res.Edges != 1 {
		t.Fatalf("nodes=%d edges=%d, want 2/1 (reverse duplicate collapsed)", res.Nodes, res.Edges)
	}
	if len(res.Dropped) < 2 {
		t.Fatalf("dropped = %v, want the ghost node and phantom edges reported", res.Dropped)
	}
}

func TestDiagramEdgeOrientationFollowsStoredRelation(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: `{"nodes":[{"id":"e1","label":"A"},{"id":"e2","label":"B"}],` +
		`"edges":[{"from":"e2","to":"e1","label":"calls"}]}`}}

	res, err := Diagram(context.Background(), sampleDiagramGraph(), chat, "m", "", DiagramOptions{})
	if err != nil {
		t.Fatalf("Diagram: %v", err)
	}
	if !strings.Contains(res.Mermaid, `n1 -->|"calls"| n2`) {
		t.Fatalf("edge must keep stored direction web->retriever:\n%s", res.Mermaid)
	}
}

func TestDiagramFallsBackOnBadReplies(t *testing.T) {
	cases := map[string]fakeChat{
		"garbage": {resp: llm.ChatResponse{Content: "not json at all"}},
		"error":   {err: errors.New("boom")},
		"too few": {resp: llm.ChatResponse{Content: `{"nodes":[{"id":"e1"}],"edges":[]}`}},
	}
	for name, chat := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := Diagram(context.Background(), sampleDiagramGraph(), chat, "m", "", DiagramOptions{})
			if err != nil {
				t.Fatalf("Diagram: %v", err)
			}
			if !res.Fallback || res.Reason == "" {
				t.Fatalf("want fallback with reason, got %+v", res)
			}
			if res.Nodes != 5 || res.Edges != 3 {
				t.Fatalf("fallback must render the whole slice, nodes=%d edges=%d", res.Nodes, res.Edges)
			}
			if !strings.Contains(res.Mermaid, `|"calls"|`) {
				t.Fatalf("fallback edges must use relation types:\n%s", res.Mermaid)
			}
		})
	}
}

func TestDiagramNilChatFallsBack(t *testing.T) {
	res, err := Diagram(context.Background(), sampleDiagramGraph(), nil, "m", "", DiagramOptions{})
	if err != nil {
		t.Fatalf("Diagram: %v", err)
	}
	if !res.Fallback || !strings.Contains(res.Reason, "no chat client") {
		t.Fatalf("got %+v", res)
	}
}

func TestDiagramFocusLimitsToNeighborhood(t *testing.T) {
	res, err := Diagram(context.Background(), sampleDiagramGraph(), nil, "m", "retriever", DiagramOptions{})
	if err != nil {
		t.Fatalf("Diagram: %v", err)
	}
	if res.Nodes != 3 {
		t.Fatalf("nodes=%d, want retriever + web + sqlite", res.Nodes)
	}
	if strings.Contains(res.Mermaid, "Lonely") || strings.Contains(res.Mermaid, `"LLM"`) {
		t.Fatalf("out-of-neighborhood entity leaked:\n%s", res.Mermaid)
	}
}

func TestDiagramFocusSubstringMatch(t *testing.T) {
	res, err := Diagram(context.Background(), sampleDiagramGraph(), nil, "m", "sqli", DiagramOptions{})
	if err != nil {
		t.Fatalf("Diagram: %v", err)
	}
	if !strings.Contains(res.Mermaid, "SQLite") {
		t.Fatalf("substring focus not resolved:\n%s", res.Mermaid)
	}
}

func TestDiagramUnknownFocusFailsOpen(t *testing.T) {
	res, err := Diagram(context.Background(), sampleDiagramGraph(), nil, "m", "nothing-like-this", DiagramOptions{})
	if err != nil {
		t.Fatalf("Diagram: %v", err)
	}
	if !res.Fallback || res.Mermaid != "" || !strings.Contains(res.Reason, "no entity matches") {
		t.Fatalf("got %+v", res)
	}
}

func TestDiagramMaxNodesKeepsFocusAndHighestDegree(t *testing.T) {
	res, err := Diagram(context.Background(), sampleDiagramGraph(), nil, "m", "", DiagramOptions{MaxNodes: 2})
	if err != nil {
		t.Fatalf("Diagram: %v", err)
	}
	if res.Nodes != 2 || res.Edges != 1 {
		t.Fatalf("nodes=%d edges=%d, want 2/1", res.Nodes, res.Edges)
	}
	if !strings.Contains(res.Mermaid, "Web") || !strings.Contains(res.Mermaid, "Retriever") {
		t.Fatalf("want the two highest-degree entities:\n%s", res.Mermaid)
	}
}

func TestDiagramStoreErrorPropagates(t *testing.T) {
	g := sampleDiagramGraph()
	g.err = errors.New("db down")
	if _, err := Diagram(context.Background(), g, nil, "m", "", DiagramOptions{}); err == nil {
		t.Fatal("want store error")
	}
}

func TestDiagramNilGraphAndEmptyGraphFailOpen(t *testing.T) {
	res, err := Diagram(context.Background(), nil, nil, "m", "", DiagramOptions{})
	if err != nil || !res.Fallback {
		t.Fatalf("nil graph: %+v %v", res, err)
	}
	res, err = Diagram(context.Background(), fakeDiagramGraph{}, nil, "m", "", DiagramOptions{})
	if err != nil || !res.Fallback || res.Mermaid != "" {
		t.Fatalf("empty graph: %+v %v", res, err)
	}
}

func TestDiagramEscapesLabelsAndStripsThink(t *testing.T) {
	g := fakeDiagramGraph{
		entities: []graphstore.Entity{
			{ID: "a", Name: `Say "hi" <b>`, Degree: 2},
			{ID: "b", Name: "B", Degree: 1},
		},
		relations: []graphstore.Relation{{ID: "r", Src: "a", Dst: "b", Type: "uses"}},
	}
	chat := fakeChat{resp: llm.ChatResponse{Content: `<think>{"nodes":[]}</think>{"nodes":[` +
		`{"id":"e1","label":"Say \"hi\" <b>"},{"id":"e2","label":"B"}],"edges":[{"from":"e1","to":"e2"}]}`}}
	res, err := Diagram(context.Background(), g, chat, "m", "", DiagramOptions{})
	if err != nil {
		t.Fatalf("Diagram: %v", err)
	}
	if res.Fallback {
		t.Fatalf("unexpected fallback: %s", res.Reason)
	}
	if !strings.Contains(res.Mermaid, `["Say #quot;hi#quot; #lt;b#gt;"]`) {
		t.Fatalf("label not escaped:\n%s", res.Mermaid)
	}
	if !strings.Contains(res.Mermaid, `n1 -->|"uses"| n2`) {
		t.Fatalf("empty edge label must default to relation type:\n%s", res.Mermaid)
	}
}
