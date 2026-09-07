package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/alterfo/kb/internal/bench/corpus"
	runbench "github.com/alterfo/kb/internal/bench/run"
)

func runBenchScoreCmd(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench score", flag.ContinueOnError)
	fset.SetOutput(stderr)
	questionsPath := fset.String("questions", "", "questions JSONL path (required)")
	out := fset.String("out", "", "score report JSON output path (default: <submission>.score.json)")
	historyPath := fset.String("history", "", "score history JSON path (default: <out>.history.json)")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	rest := fset.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "bench score: expected one argument: <submission.jsonl>")
		return 2
	}
	if *questionsPath == "" {
		fmt.Fprintln(stderr, "bench score: -questions is required")
		return 2
	}

	submission, err := runbench.LoadSubmission(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "bench score: %v\n", err)
		return 1
	}

	questions, qwarns, err := corpus.LoadQuestions(*questionsPath)
	if err != nil {
		fmt.Fprintf(stderr, "bench score: %v\n", err)
		return 1
	}
	for _, w := range qwarns {
		fmt.Fprintf(stdout, "bench score: questions warning: %s\n", w)
	}

	rep, err := runbench.Score(submission, questions)
	if err != nil {
		fmt.Fprintf(stderr, "bench score: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench score: %s\n", rep.Summary())

	outPath := *out
	if outPath == "" {
		outPath = rest[0] + ".score.json"
	}
	if err := runbench.SaveScoreReport(outPath, rep); err != nil {
		fmt.Fprintf(stderr, "bench score: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench score: report written to %s\n", outPath)

	scoreHistoryPath := *historyPath
	if scoreHistoryPath == "" {
		scoreHistoryPath = outPath + ".history.json"
	}
	if err := runbench.AppendScoreHistory(scoreHistoryPath, rep); err != nil {
		fmt.Fprintf(stderr, "bench score: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bench score: metrics history written to %s\n", scoreHistoryPath)
	return 0
}
