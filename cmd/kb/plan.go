package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/planner"
)

type planEnv struct {
	baseURL     string
	apiKey      string
	model       string
	plansDir    string
	progressDir string
	workDir     string
}

func planConfigFromEnv(env config.Env) planEnv {
	p := planEnv{
		baseURL:     env.LLMBaseURL,
		model:       env.LLMModel,
		plansDir:    "docs/plans",
		progressDir: ".planner/progress",
	}
	if wd, err := os.Getwd(); err == nil {
		p.workDir = wd
	}
	if v, ok := os.LookupEnv("KB_PLAN_BASE_URL"); ok && v != "" {
		p.baseURL = v
	}
	if v, ok := os.LookupEnv("KB_PLAN_API_KEY"); ok && v != "" {
		p.apiKey = v
	}
	if v, ok := os.LookupEnv("KB_PLAN_MODEL"); ok && v != "" {
		p.model = v
	}
	if v, ok := os.LookupEnv("KB_PLAN_DIR"); ok && v != "" {
		p.plansDir = v
	}
	if v, ok := os.LookupEnv("KB_PLAN_PROGRESS_DIR"); ok && v != "" {
		p.progressDir = v
	}
	return p
}

func runPlanCmd(args []string, env config.Env, stdout, stderr *os.File) int {
	cfg := planConfigFromEnv(env)
	maxIter := 50
	noCommit := false
	var newDesc string
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--new":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "plan --new requires a description")
				return 2
			}
			i++
			newDesc = args[i]
		case "--max-iterations":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "--max-iterations requires a value")
				return 2
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintf(stderr, "invalid --max-iterations %q\n", args[i])
				return 2
			}
			maxIter = n
		case "--no-commit":
			noCommit = true
		default:
			positional = append(positional, args[i])
		}
	}

	chat := llm.NewClient(llm.Config{BaseURL: cfg.baseURL, APIKey: cfg.apiKey})
	runner := planner.New(planner.Config{
		Model:         cfg.model,
		Chat:          chat,
		WorkDir:       cfg.workDir,
		PlansDir:      cfg.plansDir,
		ProgressDir:   cfg.progressDir,
		MaxIterations: maxIter,
		DefaultBranch: "main",
		Commit:        !noCommit,
	})

	ctx := context.Background()
	if newDesc != "" {
		path, err := runner.MakePlan(ctx, newDesc)
		if err != nil {
			fmt.Fprintf(stderr, "plan: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "plan created: %s\n", path)
		return 0
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: kb plan [--new <description>] [--no-commit] <plan-file>")
		return 2
	}

	res, err := runner.Execute(ctx, positional[0])
	if err != nil {
		fmt.Fprintf(stderr, "plan: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "plan complete: %s (iterations=%d commits=%d)\n", res.PlanPath, res.Iterations, res.Commits)
	return 0
}
