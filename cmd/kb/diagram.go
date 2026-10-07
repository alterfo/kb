package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/report"
)

type diagramParams struct {
	Focus    string
	Hops     int
	MaxNodes int
	Model    string
	Fenced   bool
	NoLLM    bool
}

func runDiagramCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("diagram", flag.ContinueOnError)
	fset.SetOutput(stderr)
	hops := fset.Int("hops", 1, "neighborhood radius around the focus entity")
	maxNodes := fset.Int("max", 24, "maximum number of nodes in the diagram")
	fenced := fset.Bool("fenced", false, "wrap the output in a ```mermaid fence")
	noLLM := fset.Bool("no-llm", false, "skip LLM grouping and labeling, render the raw slice")
	out := fset.String("o", "", "write the diagram to this file instead of stdout")
	if err := fset.Parse(args); err != nil {
		return 2
	}

	bundle, err := newEngineBundle(env)
	if err != nil {
		fmt.Fprintf(stderr, "diagram: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()

	params := diagramParams{
		Focus:    strings.Join(fset.Args(), " "),
		Hops:     *hops,
		MaxNodes: *maxNodes,
		Model:    env.LLMModel,
		Fenced:   *fenced,
		NoLLM:    *noLLM,
	}
	var chat report.ChatClient = bundle.chat
	if params.NoLLM {
		chat = nil
	}

	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(stderr, "diagram: %v\n", err)
			return 1
		}
		defer f.Close()
		w = f
	}
	return runDiagram(context.Background(), bundle.graph, chat, params, w, stderr)
}

func runDiagram(ctx context.Context, g report.DiagramGraph, chat report.ChatClient, p diagramParams, stdout, stderr io.Writer) int {
	res, err := report.Diagram(ctx, g, chat, p.Model, p.Focus, report.DiagramOptions{Hops: p.Hops, MaxNodes: p.MaxNodes})
	if err != nil {
		fmt.Fprintf(stderr, "diagram: %v\n", err)
		return 1
	}
	if res.Reason != "" {
		fmt.Fprintf(stderr, "diagram: %s\n", res.Reason)
	}
	for _, d := range res.Dropped {
		fmt.Fprintf(stderr, "diagram: dropped %s\n", d)
	}
	if res.Mermaid == "" {
		return 1
	}
	if p.Fenced {
		fmt.Fprintf(stdout, "```mermaid\n%s```\n", res.Mermaid)
	} else {
		fmt.Fprint(stdout, res.Mermaid)
	}
	fmt.Fprintf(stderr, "diagram: %d nodes, %d edges\n", res.Nodes, res.Edges)
	return 0
}
