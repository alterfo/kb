package gitlab

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/render"
)

func goldenPath(name string) string {
	return filepath.Join("testdata", name)
}

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(goldenPath(name))
	if err != nil {
		t.Fatalf("reading golden %s: %v", name, err)
	}
	if string(got) != string(want) {
		t.Fatalf("render mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestBuildIssueDocument_FrontmatterGolden(t *testing.T) {
	it := apiIssue{
		IID:         42,
		Title:       "Fix the thing",
		State:       "opened",
		WebURL:      "https://gitlab.com/acme/widgets/-/issues/42",
		UpdatedAt:   time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		Description: "Steps to reproduce.\n\nMore detail.",
		Author:      apiUser{Username: "octocat"},
		Labels:      []string{"priority:high", "bug"},
	}

	d := buildIssueDocument("main-group", "acme/widgets", it)
	if d.Kind != "issue" {
		t.Fatalf("Kind = %q, want issue", d.Kind)
	}
	if d.ID != "acme/widgets#42" {
		t.Fatalf("ID = %q, want acme/widgets#42", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "issue.md", got)
}

func TestBuildMergeRequestDocument_FrontmatterGolden(t *testing.T) {
	mr := apiMergeRequest{
		IID:         7,
		Title:       "Add feature",
		State:       "merged",
		WebURL:      "https://gitlab.com/acme/widgets/-/merge_requests/7",
		UpdatedAt:   time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		Description: "Implements the feature.",
		Author:      apiUser{Username: "hubot"},
		Labels:      []string{"enhancement"},
	}

	d := buildMergeRequestDocument("main-group", "acme/widgets", mr)
	if d.Kind != "mr" {
		t.Fatalf("Kind = %q, want mr", d.Kind)
	}
	if d.ID != "acme/widgets!7" {
		t.Fatalf("ID = %q, want acme/widgets!7", d.ID)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "mr.md", got)
}

func TestSortedLabels_SortedAndNilWhenEmpty(t *testing.T) {
	if got := sortedLabels(nil); got != nil {
		t.Fatalf("sortedLabels(nil) = %v, want nil", got)
	}
	got := sortedLabels([]string{"zeta", "alpha"})
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("sortedLabels = %v, want sorted [alpha zeta]", got)
	}
}
