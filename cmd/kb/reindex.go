package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/alterfo/kb/internal/config"
)

func runReindexCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("reindex", flag.ContinueOnError)
	fset.SetOutput(stderr)
	reembed := fset.Bool("reembed", false, "clear stored embeddings and dimension, then re-embed from scratch")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	var path string
	if fset.NArg() > 0 {
		path = fset.Arg(0)
	}

	ctx := context.Background()
	bundle, err := newEngineBundle(env)
	if err != nil {
		fmt.Fprintf(stderr, "reindex: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()
	if *reembed {
		if err := bundle.vector.Reembed(ctx); err != nil {
			fmt.Fprintf(stderr, "reindex: reembed: %v\n", err)
			return 1
		}
	}

	res, err := bundle.indexer.Reindex(ctx, path)
	if err != nil {
		fmt.Fprintf(stderr, "reindex: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "reindex: indexed=%d skipped=%d removed=%d\n", res.Indexed, res.Skipped, res.Removed)
	return 0
}
