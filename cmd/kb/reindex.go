package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/store/sqlite"
)

func runReindexCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("reindex", flag.ContinueOnError)
	fset.SetOutput(stderr)
	reembed := fset.Bool("reembed", false, "clear stored embeddings and dimension, then re-embed from scratch")
	embedModel := fset.String("embed-model", "", "embedding model to use for a shadow reindex (requires --into)")
	into := fset.String("into", "", "write a shadow reindex to this db path instead of the live kb.db")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if *embedModel != "" && *into == "" {
		fmt.Fprintln(stderr, "reindex: --embed-model requires --into")
		return 2
	}
	if *into != "" && fset.NArg() > 0 {
		fmt.Fprintln(stderr, "reindex: --into builds a full shadow index and does not accept a positional path")
		return 2
	}
	var path string
	if fset.NArg() > 0 {
		path = fset.Arg(0)
	}

	ctx := context.Background()
	if *into != "" {
		return runShadowReindexCmd(ctx, env, *into, *embedModel, stdout, stderr)
	}

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

func runShadowReindexCmd(ctx context.Context, env config.Env, into, embedModel string, stdout, stderr io.Writer) int {
	shadowEnv := env
	if embedModel != "" {
		shadowEnv.EmbedModel = embedModel
	}

	bundle, err := newEngineBundleAt(shadowEnv, into)
	if err != nil {
		fmt.Fprintf(stderr, "reindex: opening shadow db: %v\n", err)
		return 1
	}
	defer bundle.close()

	if embedModel != "" {
		if err := bundle.vector.Reembed(ctx); err != nil {
			fmt.Fprintf(stderr, "reindex: clear shadow embeddings: %v\n", err)
			return 1
		}
	}

	res, err := bundle.indexer.Reindex(ctx, "")
	if err != nil {
		fmt.Fprintf(stderr, "reindex: shadow reindex: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "shadow reindex: wrote=%s indexed=%d skipped=%d removed=%d\n",
		into, res.Indexed, res.Skipped, res.Removed)

	current := filepath.Join(env.PersistDir, "kb.db")
	if err := printIndexComparison(ctx, current, into, stdout); err != nil {
		fmt.Fprintf(stderr, "reindex: comparing indexes: %v\n", err)
		return 1
	}
	return 0
}

func printIndexComparison(ctx context.Context, currentPath, shadowPath string, stdout io.Writer) error {
	fmt.Fprintln(stdout, "index comparison:")
	current, currentExists, err := printIndexStats(ctx, stdout, "current", currentPath)
	if err != nil {
		return err
	}
	shadow, shadowExists, err := printIndexStats(ctx, stdout, "shadow", shadowPath)
	if err != nil {
		return err
	}
	if !currentExists || !shadowExists {
		return nil
	}

	type diff struct {
		name  string
		left  string
		right string
	}
	var diffs []diff
	if current.CorpusVersion != shadow.CorpusVersion {
		diffs = append(diffs, diff{"corpus_version", fmt.Sprint(current.CorpusVersion), fmt.Sprint(shadow.CorpusVersion)})
	}
	if current.HasEmbedDim && shadow.HasEmbedDim && current.EmbedDim != shadow.EmbedDim {
		diffs = append(diffs, diff{"embed_dim", fmt.Sprint(current.EmbedDim), fmt.Sprint(shadow.EmbedDim)})
	}
	if current.Chunks != shadow.Chunks {
		diffs = append(diffs, diff{"chunks", fmt.Sprint(current.Chunks), fmt.Sprint(shadow.Chunks)})
	}
	if current.EmbeddedChunks != shadow.EmbeddedChunks {
		diffs = append(diffs, diff{"embedded_chunks", fmt.Sprint(current.EmbeddedChunks), fmt.Sprint(shadow.EmbeddedChunks)})
	}
	if current.Entities != shadow.Entities {
		diffs = append(diffs, diff{"entities", fmt.Sprint(current.Entities), fmt.Sprint(shadow.Entities)})
	}
	if current.Relations != shadow.Relations {
		diffs = append(diffs, diff{"relations", fmt.Sprint(current.Relations), fmt.Sprint(shadow.Relations)})
	}
	if current.Communities != shadow.Communities {
		diffs = append(diffs, diff{"communities", fmt.Sprint(current.Communities), fmt.Sprint(shadow.Communities)})
	}
	if len(diffs) == 0 {
		fmt.Fprintln(stdout, "  diff: none")
		return nil
	}
	for _, d := range diffs {
		fmt.Fprintf(stdout, "  diff: %s current=%s shadow=%s\n", d.name, d.left, d.right)
	}
	return nil
}

func printIndexStats(ctx context.Context, stdout io.Writer, label, path string) (sqlite.IndexStats, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(stdout, "  %s: no index at %s\n", label, path)
			return sqlite.IndexStats{}, false, nil
		}
		return sqlite.IndexStats{}, false, fmt.Errorf("inspect %s index %q: %w", label, path, err)
	}
	stats, err := loadIndexStats(ctx, path)
	if err != nil {
		return sqlite.IndexStats{}, false, err
	}
	dim := "(unset)"
	if stats.HasEmbedDim {
		dim = fmt.Sprint(stats.EmbedDim)
	}
	fmt.Fprintf(stdout, "  %s: corpus_version=%d embed_dim=%s chunks=%d embedded=%d entities=%d relations=%d communities=%d\n",
		label, stats.CorpusVersion, dim, stats.Chunks, stats.EmbeddedChunks, stats.Entities, stats.Relations, stats.Communities)
	return stats, true, nil
}

func loadIndexStats(ctx context.Context, path string) (sqlite.IndexStats, error) {
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		return sqlite.IndexStats{}, err
	}
	defer db.Close()
	return db.IndexStats(ctx)
}
