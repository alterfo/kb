package governance

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

func TestProposeRetirementSplitsBySource(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "note"})
	writeDoc(t, root, "github/123.md", connector.Document{ID: "123", Source: "github", Body: "issue"})
	g := New(root, &fakeIndexer{}, nil, "")

	plan := g.ProposeRetirement([]string{"notes/a.md", "github/123.md"}, "notes/final.md")

	if len(plan.Notes) != 1 || plan.Notes[0] != "notes/a.md" {
		t.Fatalf("Notes = %v, want [notes/a.md]", plan.Notes)
	}
	if len(plan.Upstream) != 1 {
		t.Fatalf("Upstream = %+v, want 1 candidate", plan.Upstream)
	}
	c := plan.Upstream[0]
	if c.Path != "github/123.md" || c.Source != "github" || c.ID != "123" {
		t.Fatalf("Upstream[0] = %+v", c)
	}
	if len(plan.Skipped) != 0 {
		t.Fatalf("Skipped = %v, want none", plan.Skipped)
	}
}

func TestProposeRetirementSkipsApprovedNoteAndDuplicates(t *testing.T) {
	root := t.TempDir()
	g := New(root, &fakeIndexer{}, nil, "")

	plan := g.ProposeRetirement([]string{"notes/final.md", "notes/a.md", "notes/a.md"}, "notes/final.md")
	if len(plan.Notes) != 1 || plan.Notes[0] != "notes/a.md" {
		t.Fatalf("Notes = %v, want [notes/a.md] (approved note skipped, dup collapsed)", plan.Notes)
	}
}

func TestProposeRetirementSkipsUnrecoverableID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "github/no-frontmatter.md", "just a body, no frontmatter")
	g := New(root, &fakeIndexer{}, nil, "")

	plan := g.ProposeRetirement([]string{"github/no-frontmatter.md"}, "")
	if len(plan.Upstream) != 0 {
		t.Fatalf("Upstream = %+v, want none", plan.Upstream)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0] != "github/no-frontmatter.md" {
		t.Fatalf("Skipped = %v, want [github/no-frontmatter.md]", plan.Skipped)
	}
}

func TestProposeRetirementSkipsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	g := New(root, &fakeIndexer{}, nil, "")

	plan := g.ProposeRetirement([]string{"/etc/passwd"}, "")
	if len(plan.Skipped) != 1 || plan.Skipped[0] != "/etc/passwd" {
		t.Fatalf("Skipped = %v, want [/etc/passwd]", plan.Skipped)
	}
}

func TestApplyRetirementTrashesNotesAndTombstonesUpstream(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "note"})
	writeDoc(t, root, "github/123.md", connector.Document{ID: "123", Source: "github", Body: "issue"})
	idx := &fakeIndexer{}
	g := New(root, idx, nil, "")
	ts := &fakeTombstones{}

	results := g.ApplyRetirement(context.Background(), ts,
		[]string{"notes/a.md"},
		[]RetirementCandidate{{Path: "github/123.md", Source: "github", ID: "123"}},
	)

	if len(results) != 2 {
		t.Fatalf("results = %+v, want 2", results)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("result not OK: %+v", r)
		}
	}
	mustNotExist(t, root, "notes/a.md")
	mustExist(t, root, ".trash/notes/a.md")
	mustNotExist(t, root, "github/123.md")
	if ts.added["github"] != "123" {
		t.Fatalf("tombstones = %+v, want github:123", ts.added)
	}
	found := false
	for _, p := range idx.removed {
		if p == "github/123.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("index removals = %v, want github/123.md among them", idx.removed)
	}
}

func TestApplyRetirementOneBadItemDoesNotAbortRest(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "note"})
	idx := &fakeIndexer{}
	g := New(root, idx, nil, "")
	ts := &fakeTombstones{errOn: "123"}

	results := g.ApplyRetirement(context.Background(), ts,
		[]string{"notes/a.md"},
		[]RetirementCandidate{{Path: "github/123.md", Source: "github", ID: "123"}},
	)

	if len(results) != 2 {
		t.Fatalf("results = %+v, want 2", results)
	}
	okCount := 0
	failCount := 0
	for _, r := range results {
		if r.OK {
			okCount++
		} else {
			failCount++
		}
	}
	if okCount != 1 || failCount != 1 {
		t.Fatalf("results = %+v, want 1 OK + 1 failing", results)
	}
	mustNotExist(t, root, "notes/a.md") // the good item still applied
}
