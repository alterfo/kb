package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
)

type stubDiagramGraph struct {
	entities  []graphstore.Entity
	relations []graphstore.Relation
}

func (g stubDiagramGraph) MatchEntities(context.Context, []string, ...time.Time) ([]graphstore.Entity, error) {
	return nil, nil
}

func (g stubDiagramGraph) Neighbors(context.Context, string, int, ...time.Time) ([]graphstore.Entity, []graphstore.Relation, error) {
	return g.entities, g.relations, nil
}

func (g stubDiagramGraph) AllEntities(context.Context) ([]graphstore.Entity, error) {
	return g.entities, nil
}

func (g stubDiagramGraph) AllRelations(context.Context) ([]graphstore.Relation, error) {
	return g.relations, nil
}

type stubDiagramChat struct{ reply string }

func (c stubDiagramChat) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Content: c.reply}, nil
}

func diagramFixture() stubDiagramGraph {
	return stubDiagramGraph{
		entities: []graphstore.Entity{
			{ID: "a", Name: "Web", Type: "component", Degree: 2},
			{ID: "b", Name: "Store", Type: "storage", Degree: 1},
		},
		relations: []graphstore.Relation{{ID: "r", Src: "a", Dst: "b", Type: "reads"}},
	}
}

func TestRunDiagram_PrintsMermaid(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := runDiagram(context.Background(), diagramFixture(), nil, diagramParams{Model: "m"}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errBuf.String())
	}
	if !strings.HasPrefix(out.String(), "flowchart LR") || !strings.Contains(out.String(), `|"reads"|`) {
		t.Fatalf("stdout:\n%s", out.String())
	}
}

func TestRunDiagram_Fenced(t *testing.T) {
	var out, errBuf bytes.Buffer
	runDiagram(context.Background(), diagramFixture(), nil, diagramParams{Model: "m", Fenced: true}, &out, &errBuf)
	if !strings.HasPrefix(out.String(), "```mermaid\nflowchart LR") || !strings.HasSuffix(out.String(), "```\n") {
		t.Fatalf("stdout:\n%s", out.String())
	}
}

func TestRunDiagram_UsesLLMPlan(t *testing.T) {
	chat := stubDiagramChat{reply: `{"nodes":[{"id":"e1","label":"Dashboard"},{"id":"e2","label":"Database"}],"edges":[{"from":"e1","to":"e2","label":"queries"}]}`}
	var out, errBuf bytes.Buffer
	code := runDiagram(context.Background(), diagramFixture(), chat, diagramParams{Model: "m"}, &out, &errBuf)
	if code != 0 || !strings.Contains(out.String(), `["Dashboard"]`) || !strings.Contains(out.String(), `|"queries"|`) {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errBuf.String())
	}
}

func TestRunDiagram_EmptyGraphExitsOne(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := runDiagram(context.Background(), stubDiagramGraph{}, nil, diagramParams{Model: "m"}, &out, &errBuf)
	if code != 1 || out.Len() != 0 || !strings.Contains(errBuf.String(), "empty") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out.String(), errBuf.String())
	}
}
