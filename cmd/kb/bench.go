package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alterfo/kb/internal/bench/corpus"
	runbench "github.com/alterfo/kb/internal/bench/run"
	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
)

func runBenchCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "compare" {
		return runBenchCompareCmd(args[1:], stdout, stderr)
	}
	fset := flag.NewFlagSet("bench", flag.ContinueOnError)
	fset.SetOutput(stderr)
	corpusDir := fset.String("corpus", "", "benchmark corpus root (directory tree of .txt/.json docs)")
	questionsPath := fset.String("questions", "", "questions JSONL path")
	out := fset.String("out", "answers.jsonl", "submission answers JSONL output path")
	reportOut := fset.String("report", "", "per-type metrics report JSON path (default: <out>.report.json)")
	limit := fset.Int("limit", 0, "evaluate only the first N questions (0 = all)")
	typesCSV := fset.String("types", "", "comma-separated question types to include (empty = all)")
	concurrency := fset.Int("concurrency", 1, "questions evaluated in parallel")
	topK := fset.Int("top-k", env.TopK, "chunks retrieved per subgoal")
	persistDir := fset.String("persist-dir", "", "reuse this persist/root dir instead of a temporary one; unchanged docs skip reindexing via doc_hashes")
	historyPath := fset.String("history", "", "metrics history JSON path (default: persist-dir/bench-history.json, or out.history.json)")
	smoke := fset.Bool("smoke", false, "use the checked-in testdata/lang-bench subset for a one-minute sanity run")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if *smoke {
		if *corpusDir == "" {
			*corpusDir = filepath.Join("testdata", "lang-bench", "corpus")
		}
		if *questionsPath == "" {
			*questionsPath = filepath.Join("testdata", "lang-bench", "questions.jsonl")
		}
	}
	if *corpusDir == "" || *questionsPath == "" {
		fmt.Fprintln(stderr, "bench: -corpus and -questions are required")
		return 2
	}

	ctx := context.Background()
	docs, warns, err := corpus.LoadCorpus(*corpusDir)
	if err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}
	for _, w := range warns {
		fmt.Fprintf(stdout, "bench: corpus warning: %s\n", w)
	}

	questions, qwarns, err := corpus.LoadQuestions(*questionsPath)
	if err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}
	for _, w := range qwarns {
		fmt.Fprintf(stdout, "bench: questions warning: %s\n", w)
	}
	questions = runbench.FilterQuestions(questions, csvSet(*typesCSV), *limit)
	if len(questions) == 0 {
		fmt.Fprintln(stdout, "bench: no questions matched the filters")
		return 0
	}

	benchEnv, cleanup, err := benchIsolatedEnv(env, *persistDir)
	if err != nil {
		fmt.Fprintf(stderr, "bench: create temporary persist dir: %v\n", err)
		return 1
	}
	defer cleanup()

	bundle, err := newEngineBundle(benchEnv)
	if err != nil {
		fmt.Fprintf(stderr, "bench: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()

	bundle.updater.BeginBulk()
	indexed := 0
	skipped := 0
	for _, d := range docs {
		changed, err := bundle.indexer.IndexDocumentIfChanged(ctx, d.ToDocument())
		if err != nil {
			fmt.Fprintf(stderr, "bench: index %s: %v\n", d.ID, err)
			return 1
		}
		if changed {
			indexed++
		} else {
			skipped++
		}
	}
	if err := bundle.updater.EndBulk(ctx); err != nil {
		fmt.Fprintf(stderr, "bench: finalize graph communities: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench: indexed %d documents, skipped %d unchanged\n", indexed, skipped)

	if err := bundle.bm25.Refresh(ctx, bundle.db, bundle.vector); err != nil {
		fmt.Fprintf(stderr, "bench: refresh bm25: %v\n", err)
		return 1
	}

	r := benchRetriever(env, bundle)
	orch := got.New(got.Config{
		Retriever:         retriever.Adapter{Retriever: r},
		Chat:              bundle.chat,
		Model:             env.LLMModel,
		K:                 *topK,
		MaxSubgoals:       env.MaxSubgoals,
		MaxGapQueries:     env.MaxGapQueries,
		RollingMemory:     env.AskRollingWindow,
		ExtractQualifiers: env.QualifierFilter,
		AbstainThreshold:  env.AbstainThreshold,
	})

	runner := &runbench.Runner{
		Questions:   questions,
		OutPath:     *out,
		Concurrency: *concurrency,
		Ask: func(ctx context.Context, q corpus.Question) (string, []string) {
			g := orch.Run(ctx, q.Text)
			docIDs := make([]string, 0, len(g.Sources))
			for _, s := range g.Sources {
				docIDs = append(docIDs, s.DocID)
			}
			return g.FinalAnswer, runbench.CorpusDocumentIDs(docIDs)
		},
	}

	rep, err := runner.Run(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}

	reportPath := *reportOut
	if reportPath == "" {
		reportPath = *out + ".report.json"
	}
	if err := runbench.SaveReport(reportPath, rep); err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}

	metricsHistoryPath := *historyPath
	if metricsHistoryPath == "" {
		if *persistDir != "" {
			metricsHistoryPath = filepath.Join(*persistDir, "bench-history.json")
		} else {
			metricsHistoryPath = *out + ".history.json"
		}
	}
	if err := runbench.AppendReportHistory(metricsHistoryPath, rep); err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "bench: %s\n", rep.Summary())
	fmt.Fprintf(stdout, "bench: answers written to %s\n", *out)
	fmt.Fprintf(stdout, "bench: report written to %s\n", reportPath)
	fmt.Fprintf(stdout, "bench: metrics history written to %s\n", metricsHistoryPath)
	return 0
}

func benchRetriever(env config.Env, bundle *engineBundle) *retriever.Retriever {
	return retriever.New(retriever.Config{
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
		SetMaxRounds:   env.SetMaxRounds,
		SupersedeMode:  retriever.SupersedeMode(env.SupersedeMode),
		ANNPrefilter:   env.ANNPrefilter,
	})
}

func benchIsolatedEnv(env config.Env, persistDir string) (config.Env, func(), error) {
	if persistDir != "" {
		if err := os.MkdirAll(persistDir, 0o755); err != nil {
			return env, func() {}, err
		}
		env.PersistDir = persistDir
		env.KBRoot = persistDir
		return env, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "kb-bench-*")
	if err != nil {
		return env, func() {}, err
	}
	env.PersistDir = dir
	env.KBRoot = dir
	return env, func() { _ = os.RemoveAll(dir) }, nil
}

func csvSet(csv string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, part := range strings.Split(csv, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out[p] = struct{}{}
		}
	}
	return out
}

func runBenchCompareCmd(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench compare", flag.ContinueOnError)
	fset.SetOutput(stderr)
	out := fset.String("out", "", "optional JSON output path for the compare result")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	rest := fset.Args()
	if len(rest) != 2 {
		fmt.Fprintln(stderr, "bench compare: expected two report files: <baseline.report.json> <candidate.report.json>")
		return 2
	}

	baseline, err := runbench.LoadReport(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "bench compare: %v\n", err)
		return 2
	}
	candidate, err := runbench.LoadReport(rest[1])
	if err != nil {
		fmt.Fprintf(stderr, "bench compare: %v\n", err)
		return 2
	}

	result := runbench.Compare(baseline, candidate)
	fmt.Fprintln(stdout, result.String())
	if *out != "" {
		if err := runbench.SaveCompareResult(*out, &result); err != nil {
			fmt.Fprintf(stderr, "bench compare: %v\n", err)
			return 1
		}
	}
	return 0
}
