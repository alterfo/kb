package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/alterfo/kb/internal/bench/actualize"
	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
)

func runBenchActualizeCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench-actualize", flag.ContinueOnError)
	fset.SetOutput(stderr)
	out := fset.String("out", "docs/bench/actualization/run.json", "JSON report output path")
	persistDir := fset.String("persist-dir", "", "optional persist/root dir (defaults to an isolated temp dir)")
	topK := fset.Int("top-k", env.TopK, "chunks retrieved per subgoal")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if fset.NArg() != 0 {
		fmt.Fprintln(stderr, "bench-actualize: unexpected arguments")
		return 2
	}

	benchEnv, cleanup, err := actualizeIsolatedEnv(env, *persistDir)
	if err != nil {
		fmt.Fprintf(stderr, "bench-actualize: create isolated dir: %v\n", err)
		return 1
	}
	defer cleanup()

	ctx := context.Background()
	bundle, err := newEngineBundle(benchEnv)
	if err != nil {
		fmt.Fprintf(stderr, "bench-actualize: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()

	docs := actualize.SeedDocuments()
	bundle.updater.BeginBulk()
	for _, d := range docs {
		if err := bundle.indexer.IndexDocument(ctx, d); err != nil {
			fmt.Fprintf(stderr, "bench-actualize: index seed %q: %v\n", d.ID, err)
			return 1
		}
	}
	if err := bundle.updater.EndBulk(ctx); err != nil {
		fmt.Fprintf(stderr, "bench-actualize: finalize graph communities: %v\n", err)
		return 1
	}
	if err := bundle.bm25.Refresh(ctx, bundle.db, bundle.vector); err != nil {
		fmt.Fprintf(stderr, "bench-actualize: refresh bm25: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench-actualize: indexed %d seed documents\n", len(docs))

	orch := actualizeOrchestrator(env, bundle, *topK)
	questions := actualize.Questions()

	fmt.Fprintln(stdout, "bench-actualize: answering before corpus update...")
	before := askActualizeQuestions(ctx, orch, questions, stdout)

	fixture := httptest.NewServer(actualize.NewFixtureHandler())
	defer fixture.Close()

	if err := writeActualizeSources(benchEnv.KBRoot, fixture.URL); err != nil {
		fmt.Fprintf(stderr, "bench-actualize: write sources.yaml: %v\n", err)
		return 1
	}
	restoreToken := setActualizeSlackToken()
	defer restoreToken()

	if code := runSyncCmd([]string{"--all"}, benchEnv, stdout, stderr); code != 0 {
		fmt.Fprintf(stderr, "bench-actualize: sync returned %d\n", code)
		return 1
	}

	res, err := bundle.indexer.Reindex(ctx, "")
	if err != nil {
		fmt.Fprintf(stderr, "bench-actualize: reindex corrections: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench-actualize: reindexed corrections indexed=%d skipped=%d removed=%d\n", res.Indexed, res.Skipped, res.Removed)
	if err := bundle.bm25.Refresh(ctx, bundle.db, bundle.vector); err != nil {
		fmt.Fprintf(stderr, "bench-actualize: refresh bm25 after corrections: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "bench-actualize: answering after corpus update...")
	after := askActualizeQuestions(ctx, orch, questions, stdout)

	report, err := actualize.BuildReport(before, after)
	if err != nil {
		fmt.Fprintf(stderr, "bench-actualize: score answers: %v\n", err)
		return 1
	}
	if err := actualize.SaveReport(*out, report); err != nil {
		fmt.Fprintf(stderr, "bench-actualize: write report: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "bench-actualize: report written to %s\n", *out)
	fmt.Fprintf(stdout, "bench-actualize: total=%d affected=%d control=%d before_correct=%d/%d after_correct=%d/%d affected_updated=%d/%d control_stable=%d/%d\n",
		report.Summary.Total, report.Summary.Affected, report.Summary.Control,
		report.Summary.BeforeCorrect, report.Summary.Total,
		report.Summary.AfterCorrect, report.Summary.Total,
		report.Summary.AffectedUpdated, report.Summary.Affected,
		report.Summary.ControlStable, report.Summary.Control)
	return 0
}

func actualizeIsolatedEnv(env config.Env, persistDir string) (config.Env, func(), error) {
	if persistDir != "" {
		entries, err := os.ReadDir(persistDir)
		if err != nil && !os.IsNotExist(err) {
			return env, func() {}, err
		}
		if len(entries) != 0 {
			return env, func() {}, fmt.Errorf("persist-dir %q is not empty: bench-actualize needs a fresh directory per run, since reusing one carries over sync cursors and indexed state from the earlier run", persistDir)
		}
		if err := os.MkdirAll(persistDir, 0o755); err != nil {
			return env, func() {}, err
		}
		env.PersistDir = persistDir
		env.KBRoot = persistDir
		return env, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "kb-actualize-*")
	if err != nil {
		return env, func() {}, err
	}
	env.PersistDir = dir
	env.KBRoot = dir
	return env, func() { _ = os.RemoveAll(dir) }, nil
}

func actualizeOrchestrator(env config.Env, bundle *engineBundle, topK int) *got.Orchestrator {
	r := benchRetriever(env, bundle)
	return got.New(got.Config{
		Retriever:         retriever.Adapter{Retriever: r},
		Chat:              bundle.chat,
		Model:             env.LLMModel,
		K:                 topK,
		MaxSubgoals:       env.MaxSubgoals,
		MaxGapQueries:     env.MaxGapQueries,
		RollingMemory:     env.AskRollingWindow,
		ExtractQualifiers: env.QualifierFilter,
		AbstainThreshold:  env.AbstainThreshold,
	})
}

func askActualizeQuestions(ctx context.Context, orch *got.Orchestrator, questions []actualize.QA, stdout io.Writer) []string {
	answers := make([]string, len(questions))
	for i, q := range questions {
		answers[i] = orch.Run(ctx, q.Question).FinalAnswer
		if (i+1)%5 == 0 || i+1 == len(questions) {
			fmt.Fprintf(stdout, "bench-actualize: answered %d/%d questions\n", i+1, len(questions))
		}
	}
	return answers
}

func writeActualizeSources(root, baseURL string) error {
	content := fmt.Sprintf("sources:\n  - name: avrora-slack\n    type: slack\n    config:\n      base_url: %q\n      channels: \"C-AVRORA\"\n    secrets:\n      token: SLACK_TOKEN\n", baseURL)
	return os.WriteFile(filepath.Join(root, "sources.yaml"), []byte(content), 0o644)
}

func setActualizeSlackToken() func() {
	const key = "SLACK_TOKEN"
	old, existed := os.LookupEnv(key)
	if err := os.Setenv(key, "xoxb-actualize-fixture"); err != nil {
		return func() {}
	}
	return func() {
		if existed {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	}
}
