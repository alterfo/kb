package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/alterfo/kb/internal/bench/corpus"
	runbench "github.com/alterfo/kb/internal/bench/run"
	"github.com/alterfo/kb/internal/config"
)

func runBenchEvolveCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench evolve", flag.ContinueOnError)
	fset.SetOutput(stderr)
	corpusDir := fset.String("corpus", "", "benchmark corpus root (directory tree of .txt/.json docs)")
	questionsPath := fset.String("questions", "", "questions JSONL path")
	persistA := fset.String("persist-dir-a", "", "no-graph persist dir for stages 0-1 (default: <out-dir>/persist-a)")
	persistB := fset.String("persist-dir-b", "", "graph persist dir for stages 2-6 (default: <out-dir>/persist-b)")
	outDir := fset.String("out-dir", "bench-evolve", "directory for per-stage submissions, scores and history")
	concurrency := fset.Int("concurrency", 1, "questions evaluated in parallel")
	topK := fset.Int("top-k", env.TopK, "chunks retrieved per subgoal")
	limit := fset.Int("limit", 0, "evaluate only the first N questions (0 = all)")
	typesCSV := fset.String("types", "", "comma-separated question types to include (empty = all)")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if *corpusDir == "" || *questionsPath == "" {
		fmt.Fprintln(stderr, "bench evolve: -corpus and -questions are required")
		return 2
	}

	ctx := context.Background()
	docs, warns, err := corpus.LoadCorpus(*corpusDir)
	if err != nil {
		fmt.Fprintf(stderr, "bench evolve: %v\n", err)
		return 1
	}
	for _, w := range warns {
		fmt.Fprintf(stdout, "bench evolve: corpus warning: %s\n", w)
	}

	questions, qwarns, err := corpus.LoadQuestions(*questionsPath)
	if err != nil {
		fmt.Fprintf(stderr, "bench evolve: %v\n", err)
		return 1
	}
	for _, w := range qwarns {
		fmt.Fprintf(stdout, "bench evolve: questions warning: %s\n", w)
	}
	questions = runbench.FilterQuestions(questions, csvSet(*typesCSV), *limit)
	if len(questions) == 0 {
		fmt.Fprintln(stdout, "bench evolve: no questions matched the filters")
		return 0
	}

	if *persistA == "" {
		*persistA = filepath.Join(*outDir, "persist-a")
	}
	if *persistB == "" {
		*persistB = filepath.Join(*outDir, "persist-b")
	}

	stages := runbench.EvolutionStages()
	var firstErr error
	for i, stage := range stages {
		fmt.Fprintf(stdout, "bench evolve: stage %d %s\n", i, stage.Name)
		stageEnv, err := config.OverrideEnv(env, stage.EnvOverrides)
		if err != nil {
			fmt.Fprintf(stderr, "bench evolve: stage %s: %v\n", stage.Name, err)
			return 1
		}
		persistDir := *persistA
		if stage.PersistDir == "persist-b" {
			persistDir = *persistB
		}
		outPath := filepath.Join(*outDir, fmt.Sprintf("stage%d-%s.json", i, stage.Name))
		if err := benchEvolveStage(ctx, stageEnv, stage.AnswerMode, persistDir, outPath, docs, questions, *concurrency, *topK, stdout); err != nil {
			fmt.Fprintf(stderr, "bench evolve: stage %s: %v\n", stage.Name, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		return 1
	}
	return 0
}

func benchEvolveStage(ctx context.Context, env config.Env, answerMode, persistDir, outPath string, docs []corpus.Doc, questions []corpus.Question, concurrency, topK int, stdout io.Writer) error {
	benchEnv, cleanup, err := benchIsolatedEnv(env, persistDir)
	if err != nil {
		return fmt.Errorf("create persist dir: %w", err)
	}
	defer cleanup()

	bundle, err := newEngineBundle(benchEnv)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer bundle.close()

	bundle.updater.BeginBulk()
	indexed := 0
	skipped := 0
	for _, d := range docs {
		changed, err := bundle.indexer.IndexDocumentIfChanged(ctx, d.ToDocument())
		if err != nil {
			return fmt.Errorf("index %s: %w", d.ID, err)
		}
		if changed {
			indexed++
		} else {
			skipped++
		}
	}
	if err := bundle.updater.EndBulk(ctx); err != nil {
		return fmt.Errorf("finalize graph communities: %w", err)
	}
	fmt.Fprintf(stdout, "bench evolve: indexed %d documents, skipped %d unchanged\n", indexed, skipped)

	if err := bundle.bm25.Refresh(ctx, bundle.db, bundle.vector); err != nil {
		return fmt.Errorf("refresh bm25: %w", err)
	}

	r := benchRetriever(benchEnv, bundle)
	ask := benchAsk(benchEnv, r, bundle.chat, bundle.bm25, answerMode, topK)

	runner := &runbench.Runner{
		Questions:   questions,
		OutPath:     outPath,
		Concurrency: concurrency,
		AskCtx:      ask,
	}

	rep, runErr := runner.Run(ctx)
	if rep == nil {
		return runErr
	}
	if err := benchEvolveScoreAndSave(outPath, questions, stdout); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	return nil
}

func benchEvolveScoreAndSave(outPath string, questions []corpus.Question, stdout io.Writer) error {
	submission, err := runbench.LoadSubmission(outPath)
	if err != nil {
		return fmt.Errorf("load submission: %w", err)
	}
	score := runbench.Score(submission, questions)
	fmt.Fprintf(stdout, "bench evolve: %s\n", score.Summary())

	scorePath := outPath + ".score.json"
	if err := runbench.SaveScoreReport(scorePath, score); err != nil {
		return fmt.Errorf("save score report: %w", err)
	}
	historyPath := scorePath + ".history.json"
	if err := runbench.AppendScoreHistory(historyPath, score); err != nil {
		return fmt.Errorf("append score history: %w", err)
	}
	fmt.Fprintf(stdout, "bench evolve: submission written to %s\n", outPath)
	fmt.Fprintf(stdout, "bench evolve: score written to %s\n", scorePath)
	return nil
}
