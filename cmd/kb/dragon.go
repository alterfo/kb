package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/bench/dragon"
	runbench "github.com/alterfo/kb/internal/bench/run"
	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
)

func runBenchDragonCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "score" {
		return runBenchDragonScoreCmd(args[1:], stdout, stderr)
	}
	fset := flag.NewFlagSet("bench-dragon", flag.ContinueOnError)
	fset.SetOutput(stderr)
	out := fset.String("out", "answers.dragon.json", "DRAGON submission JSON output path (question id -> found_ids/model_answer)")
	limit := fset.Int("limit", 0, "evaluate only the first N questions (0 = all 600)")
	concurrency := fset.Int("concurrency", 1, "questions evaluated in parallel")
	topK := fset.Int("top-k", env.TopK, "chunks retrieved per subgoal")
	baseURL := fset.String("hf-base-url", dragon.DefaultBaseURL, "HuggingFace datasets-server base URL")
	hist := fset.Bool("hist", false, "use the hist-* DRAGON variant (has an ungated gold set for local scoring via 'bench-dragon score')")
	if err := fset.Parse(args); err != nil {
		return 2
	}

	textsDataset, questionsDataset := dragon.TextsDataset, dragon.QuestionsDataset
	if *hist {
		textsDataset, questionsDataset = dragon.HistTextsDataset, dragon.HistQuestionsDataset
	}

	ctx := context.Background()
	httpClient := &http.Client{}

	fmt.Fprintln(stdout, "bench-dragon: fetching corpus from HuggingFace...")
	texts, err := dragon.FetchTexts(ctx, httpClient, *baseURL, textsDataset)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench-dragon: fetched %d texts\n", len(texts))

	fmt.Fprintln(stdout, "bench-dragon: fetching questions from HuggingFace...")
	rawQuestions, err := dragon.FetchQuestions(ctx, httpClient, *baseURL, questionsDataset)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon: %v\n", err)
		return 1
	}
	questions := dragon.ToQuestions(rawQuestions)
	if *limit > 0 && *limit < len(questions) {
		questions = questions[:*limit]
	}
	if len(questions) == 0 {
		fmt.Fprintln(stdout, "bench-dragon: no questions to evaluate")
		return 0
	}
	fmt.Fprintf(stdout, "bench-dragon: %d questions selected\n", len(questions))

	benchEnv, cleanup, err := benchIsolatedEnv(env)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon: create temporary persist dir: %v\n", err)
		return 1
	}
	defer cleanup()

	bundle, err := newEngineBundle(benchEnv)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()

	bundle.updater.BeginBulk()
	docs := dragon.ToDocuments(texts)
	indexed := 0
	for _, doc := range docs {
		if err := bundle.indexer.IndexDocument(ctx, doc); err != nil {
			fmt.Fprintf(stderr, "bench-dragon: index %s: %v\n", doc.ID, err)
			return 1
		}
		indexed++
		if indexed%25 == 0 || indexed == len(docs) {
			fmt.Fprintf(stdout, "bench-dragon: indexed %d/%d documents\n", indexed, len(docs))
		}
	}
	if err := bundle.updater.EndBulk(ctx); err != nil {
		fmt.Fprintf(stderr, "bench-dragon: finalize graph communities: %v\n", err)
		return 1
	}

	if err := bundle.bm25.Refresh(ctx, bundle.db, bundle.vector); err != nil {
		fmt.Fprintf(stderr, "bench-dragon: refresh bm25: %v\n", err)
		return 1
	}

	r := benchRetriever(env, bundle)
	orch := got.New(got.Config{
		Retriever:         retriever.Adapter{Retriever: r},
		Chat:              bundle.chat,
		Model:             env.LLMModel,
		K:                 *topK,
		RollingMemory:     env.AskRollingWindow,
		ExtractQualifiers: env.QualifierFilter,
		AbstainThreshold:  env.AbstainThreshold,
	})

	fmt.Fprintln(stdout, "bench-dragon: answering questions...")
	total := len(questions)
	entries := dragon.RunQuestions(ctx, questions, *concurrency, func(ctx context.Context, q corpus.Question) (string, []string) {
		g := orch.Run(ctx, q.Text)
		docIDs := make([]string, 0, len(g.Sources))
		for _, s := range g.Sources {
			docIDs = append(docIDs, s.DocID)
		}
		return g.FinalAnswer, runbench.CorpusDocumentIDs(docIDs)
	}, func(done int) {
		if done%25 == 0 || done == total {
			fmt.Fprintf(stdout, "bench-dragon: answered %d/%d questions\n", done, total)
		}
	})

	if err := dragon.SaveSubmission(*out, entries); err != nil {
		fmt.Fprintf(stderr, "bench-dragon: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench-dragon: submission written to %s (%d answers)\n", *out, len(entries))
	fmt.Fprintln(stdout, "bench-dragon: this is a self-run submission file (kb's own found_ids/model_answer), not an official DRAGON leaderboard score")
	return 0
}

func runBenchDragonScoreCmd(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench-dragon score", flag.ContinueOnError)
	fset.SetOutput(stderr)
	out := fset.String("out", "", "optional score report JSON output path")
	baseURL := fset.String("hf-base-url", dragon.DefaultBaseURL, "HuggingFace datasets-server base URL")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	rest := fset.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "bench-dragon score: expected one argument: <submission.json> (must be a --hist run to match the gold set)")
		return 2
	}

	submission, err := dragon.LoadSubmission(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon score: %v\n", err)
		return 1
	}

	ctx := context.Background()
	httpClient := &http.Client{}
	fmt.Fprintln(stdout, "bench-dragon score: fetching gold QA from HuggingFace...")
	gold, err := dragon.FetchGoldQA(ctx, httpClient, *baseURL, dragon.HistGoldDataset)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon score: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench-dragon score: fetched %d gold QA pairs\n", len(gold))

	rep, err := dragon.Score(submission, gold)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon score: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench-dragon score: %s\n", rep.Summary())

	if *out != "" {
		if err := dragon.SaveScoreReport(*out, rep); err != nil {
			fmt.Fprintf(stderr, "bench-dragon score: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "bench-dragon score: report written to %s\n", *out)
	}
	return 0
}
