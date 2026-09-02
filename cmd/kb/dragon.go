package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/bench/dragon"
	runbench "github.com/alterfo/kb/internal/bench/run"
	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/store/sqlite"
)

func runBenchDragonCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "score" {
		return runBenchDragonScoreCmd(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "calibrate" {
		return runBenchDragonCalibrateCmd(args[1:], env, stdout, stderr)
	}
	fset := flag.NewFlagSet("bench-dragon", flag.ContinueOnError)
	fset.SetOutput(stderr)
	out := fset.String("out", "answers.dragon.json", "DRAGON submission JSON output path (question id -> found_ids/model_answer)")
	limit := fset.Int("limit", 0, "evaluate only the first N questions (0 = all 600)")
	concurrency := fset.Int("concurrency", 1, "questions evaluated in parallel")
	topK := fset.Int("top-k", env.TopK, "chunks retrieved per subgoal")
	baseURL := fset.String("hf-base-url", dragon.DefaultBaseURL, "HuggingFace datasets-server base URL")
	hist := fset.Bool("hist", false, "use the hist-* DRAGON variant (has an ungated gold set for local scoring via 'bench-dragon score')")
	persistDir := fset.String("persist-dir", "", "reuse this persist/root dir instead of a temporary one; unchanged docs skip reindexing via doc_hashes")
	forceReindex := fset.Bool("force-reindex", false, "reindex the corpus even when a persisted index already exists")
	docLimit := fset.Int("doc-limit", 0, "keep only the first N fetched texts (0 = all)")
	smoke := fset.Bool("smoke", false, "use a small fixed subset for a one-minute sanity run")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if *smoke {
		if *docLimit == 0 {
			*docLimit = 12
		}
		if *limit == 0 {
			*limit = 5
		}
	}
	if *docLimit > 0 {
		*hist = true
	}

	textsDataset, questionsDataset := dragon.TextsDataset, dragon.QuestionsDataset
	if *hist {
		textsDataset, questionsDataset = dragon.HistTextsDataset, dragon.HistQuestionsDataset
	}

	ctx := context.Background()
	httpClient := &http.Client{}

	benchEnv, cleanup, err := benchIsolatedEnv(env, *persistDir)
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

	reuseIndex, err := benchDragonReuseIndex(ctx, bundle.db, *persistDir, *forceReindex, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon: count persisted chunks: %v\n", err)
		return 1
	}

	var keptDocIDs map[string]struct{}
	if reuseIndex {
		fmt.Fprintln(stdout, "bench-dragon: skipping corpus fetch and indexing")
		if *docLimit > 0 {
			texts, err := dragon.FetchTexts(ctx, httpClient, *baseURL, textsDataset)
			if err != nil {
				fmt.Fprintf(stderr, "bench-dragon: %v\n", err)
				return 1
			}
			texts = dragon.LimitTexts(texts, *docLimit)
			keptDocIDs = dragon.TextIDSet(texts)
		}
	} else {
		fmt.Fprintf(stdout, "bench-dragon: indexing into %s\n", benchEnv.PersistDir)
		fmt.Fprintln(stdout, "bench-dragon: fetching corpus from HuggingFace...")
		texts, err := dragon.FetchTexts(ctx, httpClient, *baseURL, textsDataset)
		if err != nil {
			fmt.Fprintf(stderr, "bench-dragon: %v\n", err)
			return 1
		}
		fetchedTexts := len(texts)
		texts = dragon.LimitTexts(texts, *docLimit)
		keptDocIDs = dragon.TextIDSet(texts)
		if *docLimit > 0 {
			fmt.Fprintf(stdout, "bench-dragon: doc-limit keeps %d/%d docs\n", len(texts), fetchedTexts)
		} else {
			fmt.Fprintf(stdout, "bench-dragon: fetched %d texts\n", len(texts))
		}

		if err := benchDragonIndexTexts(ctx, bundle, texts, stdout); err != nil {
			fmt.Fprintf(stderr, "bench-dragon: %v\n", err)
			return 1
		}
	}

	if err := bundle.bm25.Refresh(ctx, bundle.db, bundle.vector); err != nil {
		fmt.Fprintf(stderr, "bench-dragon: refresh bm25: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "bench-dragon: fetching questions from HuggingFace...")
	rawQuestions, err := dragon.FetchQuestions(ctx, httpClient, *baseURL, questionsDataset)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon: %v\n", err)
		return 1
	}
	questions := dragon.ToQuestions(rawQuestions)
	if *docLimit > 0 {
		gold, err := dragon.FetchGoldQA(ctx, httpClient, *baseURL, dragon.HistGoldDataset)
		if err != nil {
			fmt.Fprintf(stderr, "bench-dragon: %v\n", err)
			return 1
		}
		matched, malformed := dragon.FilterQuestions(gold, questions, keptDocIDs)
		fmt.Fprintf(stdout, "bench-dragon: matched %d/%d questions to doc subset (%d malformed gold entries skipped)\n", len(matched), len(questions), malformed)
		questions = matched
	}
	if *limit > 0 && *limit < len(questions) {
		questions = questions[:*limit]
	}
	if len(questions) == 0 {
		fmt.Fprintln(stdout, "bench-dragon: no questions to evaluate")
		return 0
	}
	fmt.Fprintf(stdout, "bench-dragon: %d questions selected\n", len(questions))

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

func benchDragonIndexTexts(ctx context.Context, bundle *engineBundle, texts []dragon.Text, stdout io.Writer) error {
	docs := dragon.ToDocuments(texts)
	bundle.updater.BeginBulk()
	indexed := 0
	skipped := 0
	for _, doc := range docs {
		changed, indexErr := bundle.indexer.IndexDocumentIfChanged(ctx, doc)
		if indexErr != nil {
			return fmt.Errorf("index %s: %w", doc.ID, indexErr)
		}
		if changed {
			indexed++
		} else {
			skipped++
		}
		if (indexed+skipped)%25 == 0 || indexed+skipped == len(docs) {
			fmt.Fprintf(stdout, "bench-dragon: indexed %d/%d documents (%d unchanged)\n", indexed+skipped, len(docs), skipped)
		}
	}
	return bundle.updater.EndBulk(ctx)
}

func benchDragonReuseIndex(ctx context.Context, db *sqlite.DB, persistDir string, forceReindex bool, stdout io.Writer) (bool, error) {
	if persistDir == "" || forceReindex {
		return false, nil
	}
	chunkCount, err := db.ChunkCount(ctx)
	if err != nil {
		return false, err
	}
	if chunkCount > 0 {
		fmt.Fprintf(stdout, "bench-dragon: reusing persisted index at %s (%d chunks)\n", persistDir, chunkCount)
		return true, nil
	}
	return false, nil
}

func runBenchDragonCalibrateCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench-dragon calibrate", flag.ContinueOnError)
	fset.SetOutput(stderr)
	sample := fset.Int("sample", 15, "number of docs to index for the calibration measurement")
	budget := fset.Duration("budget", 45*time.Minute, "indexing budget the recommended -doc-limit must fit under")
	baseURL := fset.String("hf-base-url", dragon.DefaultBaseURL, "HuggingFace datasets-server base URL")
	if err := fset.Parse(args); err != nil {
		return 2
	}

	env.IndexGraph = true

	benchEnv, cleanup, err := benchIsolatedEnv(env, "")
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon calibrate: create temporary persist dir: %v\n", err)
		return 1
	}
	defer cleanup()

	bundle, err := newEngineBundle(benchEnv)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon calibrate: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()

	ctx := context.Background()
	httpClient := &http.Client{}

	fmt.Fprintln(stdout, "bench-dragon calibrate: fetching corpus from HuggingFace...")
	texts, err := dragon.FetchTexts(ctx, httpClient, *baseURL, dragon.HistTextsDataset)
	if err != nil {
		fmt.Fprintf(stderr, "bench-dragon calibrate: %v\n", err)
		return 1
	}
	texts = dragon.LimitTexts(texts, *sample)
	if len(texts) == 0 {
		fmt.Fprintln(stderr, "bench-dragon calibrate: no texts to index")
		return 1
	}
	fmt.Fprintf(stdout, "bench-dragon calibrate: indexing %d docs (graph enabled)...\n", len(texts))

	start := time.Now()
	if err := benchDragonIndexTexts(ctx, bundle, texts, stdout); err != nil {
		fmt.Fprintf(stderr, "bench-dragon calibrate: %v\n", err)
		return 1
	}
	elapsed := time.Since(start)

	secondsPerDoc := elapsed.Seconds() / float64(len(texts))
	recommended := dragon.MaxDocLimit(secondsPerDoc, *budget)
	fmt.Fprintf(stdout, "bench-dragon calibrate: indexed %d docs in %s (%.2f s/doc)\n", len(texts), elapsed.Round(time.Second), secondsPerDoc)
	fmt.Fprintf(stdout, "bench-dragon calibrate: largest -doc-limit under %s budget: %d\n", (*budget).Round(time.Second), recommended)
	return 0
}

func runBenchDragonScoreCmd(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench-dragon score", flag.ContinueOnError)
	fset.SetOutput(stderr)
	out := fset.String("out", "", "optional score report JSON output path")
	baseURL := fset.String("hf-base-url", dragon.DefaultBaseURL, "HuggingFace datasets-server base URL")
	historyPath := fset.String("history", "", "score metrics history JSON path (default: out.history.json or dragon-score-history.json)")
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
	scoreHistoryPath := *historyPath
	if scoreHistoryPath == "" {
		if *out != "" {
			scoreHistoryPath = *out + ".history.json"
		} else {
			scoreHistoryPath = "dragon-score-history.json"
		}
	}
	if err := dragon.AppendScoreHistory(scoreHistoryPath, rep); err != nil {
		fmt.Fprintf(stderr, "bench-dragon score: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench-dragon score: metrics history written to %s\n", scoreHistoryPath)
	return 0
}
