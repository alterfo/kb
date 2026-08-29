package planner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/llm"
)

// CommitFn commits the working tree with the given message. It is injectable
// so tests can avoid real git.
type CommitFn func(ctx context.Context, workDir, message string) error

// Config wires the planner's dependencies and tunables.
type Config struct {
	Model               string
	Chat                ChatClient
	WorkDir             string
	PlansDir            string
	ProgressDir         string
	MaxIterations       int
	MaxReviewIterations int
	DefaultBranch       string
	Commit              bool
	Prompts             prompts
	CommitFn            CommitFn
	Now                 func() time.Time
}

// Result summarises an Execute run.
type Result struct {
	PlanPath   string
	Iterations int
	Commits    int
}

// Runner orchestrates plan execution: task loop, review loop, finalize.
type Runner struct {
	cfg Config
}

func New(cfg Config) *Runner {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 50
	}
	if cfg.MaxReviewIterations <= 0 {
		cfg.MaxReviewIterations = 10
	}
	if cfg.DefaultBranch == "" {
		cfg.DefaultBranch = "main"
	}
	if cfg.Prompts.task == "" {
		cfg.Prompts = defaultPrompts()
	}
	if cfg.CommitFn == nil {
		cfg.CommitFn = defaultCommit
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Runner{cfg: cfg}
}

// Execute runs the full pipeline over an existing plan file.
func (r *Runner) Execute(ctx context.Context, planPath string) (Result, error) {
	abs, err := r.resolvePlanPath(planPath)
	if err != nil {
		return Result{}, err
	}
	progress := r.progressPath(abs)
	r.log(progress, "Plan: %s", abs)
	r.log(progress, "Started: %s", r.cfg.Now().Format(time.RFC3339))

	res := Result{PlanPath: abs}
	if err := r.runTasks(ctx, abs, progress, &res); err != nil {
		return res, err
	}
	if err := r.runReview(ctx, abs, progress); err != nil {
		return res, err
	}
	r.finalize(ctx, abs, progress)
	return res, nil
}

// MakePlan explores the codebase and writes a new plan file for the request,
// returning the path to the created plan.
func (r *Runner) MakePlan(ctx context.Context, description string) (string, error) {
	sys := render(r.cfg.Prompts.makePlan, map[string]string{
		"PLAN_DESCRIPTION": description,
		"WORK_DIR":         r.cfg.WorkDir,
		"PLANS_DIR":        r.cfg.PlansDir,
	})
	a := NewAgent(r.cfg.Chat, r.cfg.Model, sys, r.cfg.WorkDir)
	content, err := a.Run(ctx, []llm.ChatMessage{{Role: "user", Content: "Create the plan for: " + description}})
	if err != nil {
		return "", err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("plan model returned an empty plan")
	}
	dir := filepath.Join(r.cfg.WorkDir, r.cfg.PlansDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := r.cfg.Now().Format("20060102") + "-" + slug(description) + ".md"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (r *Runner) runTasks(ctx context.Context, planPath, progress string, res *Result) error {
	for i := 0; i < r.cfg.MaxIterations; i++ {
		plan := ParsePlan(readFile(planPath))
		sec := plan.FirstPending()
		if sec == nil {
			return nil
		}
		sys := render(r.cfg.Prompts.task, map[string]string{
			"WORK_DIR":      r.cfg.WorkDir,
			"PLAN_FILE":     planPath,
			"PROGRESS_FILE": progress,
			"SECTION":       plan.RawSection(sec),
			"GOAL":          sec.Title,
		})
		a := NewAgent(r.cfg.Chat, r.cfg.Model, sys, r.cfg.WorkDir)
		out, err := a.Run(ctx, []llm.ChatMessage{{Role: "user", Content: "Complete the current task section."}})
		r.log(progress, "Iteration %d: section %q err=%v", i+1, sec.Title, err)
		if err != nil {
			return fmt.Errorf("task %q: %w", sec.Title, err)
		}
		if HasSignal(out, SignalTaskFailed) {
			return fmt.Errorf("task %q failed", sec.Title)
		}
		res.Iterations++
		r.markSectionDone(planPath)
		if r.cfg.Commit {
			if dirty, derr := r.isDirty(ctx); derr == nil && dirty {
				if err := r.cfg.CommitFn(ctx, r.cfg.WorkDir, "feat: "+sec.Title); err != nil {
					return fmt.Errorf("commit after %q: %w", sec.Title, err)
				}
				res.Commits++
			}
		}
	}
	return fmt.Errorf("max iterations (%d) reached with pending tasks", r.cfg.MaxIterations)
}

func (r *Runner) markSectionDone(planPath string) {
	plan := ParsePlan(readFile(planPath))
	sec := plan.FirstPending()
	if sec == nil {
		return
	}
	plan.SetDone(sec)
	_ = os.WriteFile(planPath, plan.Bytes(), 0o644)
}

func (r *Runner) runReview(ctx context.Context, planPath, progress string) error {
	for i := 0; i < r.cfg.MaxReviewIterations; i++ {
		sys := render(r.cfg.Prompts.review, map[string]string{
			"WORK_DIR":       r.cfg.WorkDir,
			"PLAN_FILE":      planPath,
			"GOAL":           "plan execution",
			"DEFAULT_BRANCH": r.cfg.DefaultBranch,
		})
		a := NewAgent(r.cfg.Chat, r.cfg.Model, sys, r.cfg.WorkDir)
		out, err := a.Run(ctx, []llm.ChatMessage{{Role: "user", Content: "Review the changes."}})
		if err != nil {
			return fmt.Errorf("review: %w", err)
		}
		if HasSignal(out, SignalReviewDone) {
			if r.cfg.Commit {
				if dirty, _ := r.isDirty(ctx); dirty {
					if err := r.cfg.CommitFn(ctx, r.cfg.WorkDir, "fix: address review findings"); err != nil {
						return fmt.Errorf("review commit: %w", err)
					}
				}
			}
			r.log(progress, "Review: clean after %d round(s)", i+1)
			return nil
		}
	}
	return fmt.Errorf("review did not converge after %d rounds", r.cfg.MaxReviewIterations)
}

func (r *Runner) finalize(ctx context.Context, planPath, progress string) {
	plan := ParsePlan(readFile(planPath))
	pending := plan.PendingOther()
	if len(pending) == 0 {
		return
	}
	for i := range pending {
		plan.SetDone(&pending[i])
	}
	if err := os.WriteFile(planPath, plan.Bytes(), 0o644); err == nil {
		r.log(progress, "Finalize: marked %d non-task section(s) complete", len(pending))
	}
	if r.cfg.Commit {
		if dirty, err := r.isDirty(ctx); err == nil && dirty {
			_ = r.cfg.CommitFn(ctx, r.cfg.WorkDir, "chore: finalize plan")
		}
	}
}

func (r *Runner) resolvePlanPath(planPath string) (string, error) {
	if filepath.IsAbs(planPath) {
		return planPath, nil
	}
	return filepath.Join(r.cfg.WorkDir, planPath), nil
}

func (r *Runner) progressPath(planPath string) string {
	stem := strings.TrimSuffix(filepath.Base(planPath), filepath.Ext(planPath))
	return filepath.Join(r.cfg.WorkDir, r.cfg.ProgressDir, "progress-"+stem+".txt")
}

func (r *Runner) log(progress, format string, args ...any) {
	if progress == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(progress), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(progress, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format+"\n", args...)
}

func (r *Runner) isDirty(ctx context.Context) (bool, error) {
	out, err := gitRun(ctx, r.cfg.WorkDir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func defaultCommit(ctx context.Context, workDir, message string) error {
	if _, err := gitRun(ctx, workDir, "add", "-A"); err != nil {
		return err
	}
	_, err := gitRun(ctx, workDir, "commit", "-m", message)
	return err
}

func gitRun(ctx context.Context, workDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}

func readFile(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "plan"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}
