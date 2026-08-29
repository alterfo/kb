package planner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/llm"
)

type runnerFake struct {
	taskResponse   string
	reviewResponse string
	planResponse   string
	taskCalls      int
	reviewCalls    int
}

func (f *runnerFake) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	last := req.Messages[len(req.Messages)-1].Content
	switch {
	case strings.Contains(last, "Complete the current task section"):
		f.taskCalls++
		return llm.ChatResponse{Content: f.taskResponse}, nil
	case strings.Contains(last, "Review the changes"):
		f.reviewCalls++
		return llm.ChatResponse{Content: f.reviewResponse}, nil
	case strings.Contains(last, "Create the plan"):
		return llm.ChatResponse{Content: f.planResponse}, nil
	}
	return llm.ChatResponse{}, fmt.Errorf("unexpected prompt: %q", last)
}

const runnerPlan = `# Test plan

## Overview
Do things.

### Task 1: First thing
- [ ] do A
- [ ] do B

### Task 2: Second thing
- [ ] do C

## Success criteria
- [ ] run tests
`

func TestRunner_Execute_CompletesAllTasks(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "docs", "plans", "plan.md")
	os.MkdirAll(filepath.Dir(planPath), 0o755)
	os.WriteFile(planPath, []byte(runnerPlan), 0o644)

	fake := &runnerFake{taskResponse: "<<<PLANNER:ALL_TASKS_DONE>>>", reviewResponse: "<<<PLANNER:REVIEW_DONE>>>"}
	r := New(Config{
		Model:         "m",
		Chat:          fake,
		WorkDir:       dir,
		PlansDir:      "docs/plans",
		ProgressDir:   ".planner/progress",
		DefaultBranch: "main",
		Commit:        false,
	})
	res, err := r.Execute(context.Background(), planPath)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if fake.taskCalls != 2 || fake.reviewCalls != 1 {
		t.Errorf("taskCalls=%d reviewCalls=%d", fake.taskCalls, fake.reviewCalls)
	}
	b, _ := os.ReadFile(planPath)
	s := string(b)
	for _, want := range []string{"- [x] do A", "- [x] do B", "- [x] do C", "- [x] run tests"} {
		if !strings.Contains(s, want) {
			t.Errorf("plan output missing %q:\n%s", want, s)
		}
	}
}

func TestRunner_Execute_TaskFailed_Stops(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	os.WriteFile(planPath, []byte(runnerPlan), 0o644)

	fake := &runnerFake{taskResponse: "<<<PLANNER:TASK_FAILED>>>"}
	r := New(Config{Model: "m", Chat: fake, WorkDir: dir, Commit: false})
	if _, err := r.Execute(context.Background(), planPath); err == nil {
		t.Fatal("expected error for task failure")
	}
	if fake.taskCalls != 1 {
		t.Errorf("expected 1 task call, got %d", fake.taskCalls)
	}
}

func TestRunner_Execute_CommitsEachTask(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	planPath := filepath.Join(dir, "plan.md")
	os.WriteFile(planPath, []byte(runnerPlan), 0o644)
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "-m", "initial")

	var commits []string
	fake := &runnerFake{taskResponse: "<<<PLANNER:ALL_TASKS_DONE>>>", reviewResponse: "<<<PLANNER:REVIEW_DONE>>>"}
	r := New(Config{
		Model:         "m",
		Chat:          fake,
		WorkDir:       dir,
		DefaultBranch: "main",
		Commit:        true,
		CommitFn: func(ctx context.Context, wd, message string) error {
			commits = append(commits, message)
			mustGit(t, wd, "add", "-A")
			_, err := gitRun(ctx, wd, "-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "-m", message)
			return err
		},
	})
	if _, err := r.Execute(context.Background(), planPath); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("commits = %v, want 3", commits)
	}
	if commits[0] != "feat: First thing" || commits[1] != "feat: Second thing" {
		t.Errorf("task commits = %v", commits[:2])
	}
}

func TestRunner_MakePlan_WritesFile(t *testing.T) {
	dir := t.TempDir()
	fake := &runnerFake{planResponse: "# Do the thing\n\n### Task 1: build it\n- [ ] step\n"}
	r := New(Config{
		Model:         "m",
		Chat:          fake,
		WorkDir:       dir,
		PlansDir:      "docs/plans",
		DefaultBranch: "main",
		Commit:        false,
		Now:           func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) },
	})
	path, err := r.MakePlan(context.Background(), "Do the thing")
	if err != nil {
		t.Fatalf("MakePlan: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("docs", "plans", "20260820-do-the-thing.md")) {
		t.Errorf("unexpected path %q", path)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "### Task 1: build it") {
		t.Errorf("plan content = %q", b)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := gitRun(context.Background(), dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}
