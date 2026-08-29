package planner

import (
	"strings"
	"testing"
)

const samplePlan = `# Add a widget

## Overview

Build a widget.

## Implementation Steps

### Task 1: Create the widget
- [ ] write widget.go
- [ ] add a test

### Task 2: Wire it up
- [ ] register the widget

### Task 3: Verify acceptance criteria
- [ ] run full test suite

## Success criteria
- [ ] manual QA pass
`

func TestParsePlan_TitleAndSections(t *testing.T) {
	p := ParsePlan([]byte(samplePlan))
	if p.Title != "Add a widget" {
		t.Errorf("title = %q", p.Title)
	}
	if len(p.Sections) != 6 {
		t.Fatalf("expected 6 sections, got %d", len(p.Sections))
	}
	if p.Sections[0].Kind != "other" || p.Sections[0].Title != "Overview" {
		t.Errorf("sections[0] = %+v", p.Sections[0])
	}
	if p.Sections[2].Kind != "task" || p.Sections[2].Title != "Create the widget" {
		t.Errorf("sections[2] = %+v", p.Sections[2])
	}
	if len(p.Sections[2].Items) != 2 {
		t.Errorf("task 1 items = %d, want 2", len(p.Sections[2].Items))
	}
}

func TestParsePlan_IterationHeader(t *testing.T) {
	p := ParsePlan([]byte("### Iteration 7: do it\n- [ ] a step\n"))
	if p.Sections[0].Kind != "task" {
		t.Fatalf("expected task kind, got %q", p.Sections[0].Kind)
	}
	if p.Sections[0].Title != "do it" {
		t.Errorf("title = %q", p.Sections[0].Title)
	}
}

func TestFirstPending_ReturnsFirstIncomplete(t *testing.T) {
	p := ParsePlan([]byte(samplePlan))
	s := p.FirstPending()
	if s == nil {
		t.Fatal("expected a pending section")
	}
	if s.Title != "Create the widget" {
		t.Errorf("pending title = %q", s.Title)
	}
}

func TestSetDone_RendersCheckboxes(t *testing.T) {
	p := ParsePlan([]byte(samplePlan))
	s := p.FirstPending()
	if !p.SetDone(s) {
		t.Fatal("expected SetDone to change the plan")
	}
	if p.FirstPending() == nil {
		t.Fatal("expected task 2 to still be pending")
	}
	if p.FirstPending().Title != "Wire it up" {
		t.Errorf("next pending = %q", p.FirstPending().Title)
	}
	out := string(p.Bytes())
	if !strings.Contains(out, "- [x] write widget.go") {
		t.Errorf("expected completed checkbox in output:\n%s", out)
	}
}

func TestSetDone_Idempotent(t *testing.T) {
	p := ParsePlan([]byte(samplePlan))
	s := p.FirstPending()
	p.SetDone(s)
	if p.SetDone(s) {
		t.Error("expected second SetDone to report no change")
	}
}

func TestFirstPending_NilWhenAllTasksDone(t *testing.T) {
	p := ParsePlan([]byte(samplePlan))
	for p.FirstPending() != nil {
		p.SetDone(p.FirstPending())
	}
	if p.FirstPending() != nil {
		t.Fatal("expected no pending task sections")
	}
	if len(p.PendingOther()) != 1 {
		t.Fatalf("expected 1 pending non-task section, got %d", len(p.PendingOther()))
	}
	if p.PendingOther()[0].Title != "Success criteria" {
		t.Errorf("pending other title = %q", p.PendingOther()[0].Title)
	}
}

func TestParsePlan_AlreadyCompleted(t *testing.T) {
	src := "### Task 1: done\n- [x] step\n- [X] another\n"
	p := ParsePlan([]byte(src))
	if p.FirstPending() != nil {
		t.Fatal("expected no pending tasks")
	}
}
