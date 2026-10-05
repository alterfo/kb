package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alterfo/kb/internal/bench/dynamic"
	runbench "github.com/alterfo/kb/internal/bench/run"
)

func runBenchDynamicCmd(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("bench dynamic", flag.ContinueOnError)
	fset.SetOutput(stderr)
	actualization := fset.String("actualization", "", "bench-actualize run.json to score")
	questions := fset.String("questions", "", "ERB questions JSONL (with -stale and -submission)")
	stale := fset.String("stale", "", "curated stale/current spec JSON keyed by question_id")
	submission := fset.String("submission", "", "kb bench answers JSONL to score")
	out := fset.String("out", "", "optional JSON report output path")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	var rep *dynamic.Report
	switch {
	case *actualization != "":
		cases, before, after, err := dynamic.LoadActualization(*actualization)
		if err != nil {
			fmt.Fprintf(stderr, "bench dynamic: %v\n", err)
			return 1
		}
		rep = dynamic.Score(cases, before, after)
	case *questions != "" && *stale != "" && *submission != "":
		cases, err := dynamic.LoadERB(*questions, *stale)
		if err != nil {
			fmt.Fprintf(stderr, "bench dynamic: %v\n", err)
			return 1
		}
		sub, err := runbench.LoadSubmission(*submission)
		if err != nil {
			fmt.Fprintf(stderr, "bench dynamic: %v\n", err)
			return 1
		}
		after := make(map[string]string, len(sub))
		for id, a := range sub {
			after[id] = a.Answer
		}
		var unanswered []string
		for _, c := range cases {
			if _, ok := sub[c.ID]; !ok {
				unanswered = append(unanswered, c.ID)
			}
		}
		if len(unanswered) > 0 {
			fmt.Fprintf(stderr, "bench dynamic: submission has no answer for: %s\n", strings.Join(unanswered, ", "))
			return 1
		}
		rep = dynamic.Score(cases, nil, after)
	default:
		fmt.Fprintln(stderr, "bench dynamic: need -actualization, or -questions with -stale and -submission")
		return 2
	}
	fmt.Fprintln(stdout, rep.Summary())
	if *out != "" {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "bench dynamic: %v\n", err)
			return 1
		}
		if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
			fmt.Fprintf(stderr, "bench dynamic: %v\n", err)
			return 1
		}
	}
	return 0
}
