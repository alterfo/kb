package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/mcp"
)

func runMCPCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fset.SetOutput(stderr)
	if err := fset.Parse(args); err != nil {
		return 2
	}

	ctx := context.Background()
	bundle, err := newEngineBundle(env)
	if err != nil {
		fmt.Fprintf(stderr, "mcp: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()

	srv := mcp.NewServer(mcp.Deps{
		Root:                 env.KBRoot,
		Vector:               bundle.vector,
		Versioner:            bundle.db,
		BM25:                 bundle.bm25,
		Graph:                bundle.graph,
		Indexer:              bundle.indexer,
		Chat:                 bundle.chat,
		Embed:                bundle.embed,
		Reranker:             rerankFromEnv(env, bundle.chat),
		LLMModel:             env.LLMModel,
		EmbedModel:           env.EmbedModel,
		Hybrid:               env.Hybrid,
		AuthorityBonus:       env.AuthorityBonus,
		RRFK:                 env.RRFK,
		DefaultK:             env.TopK,
		CandidateK:           env.CandidateK,
		PerDocCap:            env.PerDocCap,
		SetMaxRounds:         env.SetMaxRounds,
		IntraDocBudget:       env.IntraDocBudget,
		AbstainThreshold:     env.AbstainThreshold,
		SupersedeMode:        env.SupersedeMode,
		DetectContradictions: env.DetectContradictions,
		QualifierFilter:      env.QualifierFilter,
		RollingMemory:        env.AskRollingWindow,
		SourcesPath:          filepath.Join(env.KBRoot, "sources.yaml"),
	})

	if err := srv.Run(ctx, &sdk.StdioTransport{}); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return 1
	}
	return 0
}
