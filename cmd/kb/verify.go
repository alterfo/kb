package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/report"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/verify/qa"
)

func runVerifyCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("verify", flag.ContinueOnError)
	fset.SetOutput(stderr)
	pairsPath := fset.String("pairs", "testdata/leon-qa/qa_pairs.json", "golden QA pairs JSON path")
	buildGolden := fset.Bool("build-golden", false, "build the golden QA set from closed issues in KB_ROOT")
	goldenOut := fset.String("golden-out", "testdata/leon-qa/qa_pairs.json", "golden QA pairs output path")
	reportOut := fset.String("report", filepath.Join(env.PersistDir, "last-qa-report.json"), "eval report output path")
	source := fset.String("source", "leon-ai", "source filter for building the golden set")
	limit := fset.Int("limit", 0, "evaluate only the first N pairs (0 = all)")
	topK := fset.Int("top-k", env.TopK, "chunks retrieved per question")
	if err := fset.Parse(args); err != nil {
		return 2
	}

	if *buildGolden {
		pairs, err := qa.BuildGoldenFromRoot(env.KBRoot, *source)
		if err != nil {
			fmt.Fprintf(stderr, "verify: %v\n", err)
			return 1
		}
		if err := qa.WriteQAPairs(*goldenOut, pairs); err != nil {
			fmt.Fprintf(stderr, "verify: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "verify: wrote %d QA pairs to %s\n", len(pairs), *goldenOut)
		return 0
	}

	pairs, err := qa.LoadQAPairs(*pairsPath)
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}
	if *limit > 0 && *limit < len(pairs) {
		pairs = pairs[:*limit]
	}
	if len(pairs) == 0 {
		fmt.Fprintln(stdout, "verify: no QA pairs to evaluate")
		return 0
	}

	ctx := context.Background()
	bundle, err := newEngineBundle(env)
	if err != nil {
		fmt.Fprintf(stderr, "verify: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()

	if err := bundle.bm25.Refresh(ctx, bundle.db, bundle.vector); err != nil {
		fmt.Fprintf(stderr, "verify: refresh bm25: %v\n", err)
		return 1
	}

	r := retriever.New(retriever.Config{
		Vector:         bundle.vector,
		BM25:           bundle.bm25,
		Chat:           bundle.chat,
		Embed:          bundle.embed,
		Reranker:       rerankFromEnv(env, bundle.chat),
		Graph:          bundle.graph,
		LLMModel:       env.LLMModel,
		EmbedModel:     env.EmbedModel,
		Hybrid:         env.Hybrid,
		AuthorityBonus: env.AuthorityBonus,
		RRFK:           env.RRFK,
		DefaultK:       env.TopK,
		CandidateK:     env.CandidateK,
		PerDocCap:      env.PerDocCap,
		IntraDocBudget: env.IntraDocBudget,
		SupersedeMode:  retriever.SupersedeMode(env.SupersedeMode),
		ANNPrefilter:   env.ANNPrefilter,
	})

	ask := func(ctx context.Context, question string) (qa.Answer, error) {
		chunks, err := r.Retrieve(ctx, question, retriever.Options{K: *topK})
		if err != nil {
			return qa.Answer{}, err
		}
		sources := make([]string, 0, len(chunks))
		for _, c := range chunks {
			sources = append(sources, c.Source)
		}
		return qa.Answer{Text: report.Synthesize(ctx, bundle.chat, env.LLMModel, question, chunks), Sources: sources}, nil
	}

	runner := &qa.Runner{Ask: ask, Judge: qa.NewLLMJudge(bundle.chat, env.LLMModel)}
	rep, err := runner.Run(ctx, pairs)
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}

	if err := os.MkdirAll(filepath.Dir(*reportOut), 0o755); err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}
	data, err := rep.JSON()
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*reportOut, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "verify: %s\n", rep.Summary())
	fmt.Fprintf(stdout, "verify: report written to %s\n", *reportOut)
	return 0
}
