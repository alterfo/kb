package github

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

func TestBuildIssueDocument_IssueFrontmatterGolden(t *testing.T) {
	it := apiIssue{
		Number:    42,
		Title:     "Fix the thing",
		State:     "open",
		HTMLURL:   "https://github.com/acme/widgets/issues/42",
		UpdatedAt: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		Body:      "Steps to reproduce.\n\nMore detail.",
		User:      apiUser{Login: "octocat"},
		Labels:    []apiLabel{{Name: "priority:high"}, {Name: "bug"}},
	}

	d := buildIssueDocument("main-org", "acme/widgets", it)
	if d.Kind != "issue" {
		t.Fatalf("Kind = %q, want issue", d.Kind)
	}
	if _, ok := d.Frontmatter["merged"]; ok {
		t.Fatalf("issue frontmatter should not contain merged: %+v", d.Frontmatter)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "issue.md", got)
}

func TestBuildIssueDocument_PRFrontmatterGolden(t *testing.T) {
	merged := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	it := apiIssue{
		Number:      7,
		Title:       "Add feature",
		State:       "closed",
		HTMLURL:     "https://github.com/acme/widgets/pull/7",
		UpdatedAt:   time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		Body:        "Implements the feature.",
		User:        apiUser{Login: "hubot"},
		Labels:      []apiLabel{{Name: "enhancement"}},
		PullRequest: &apiPullRequestStub{MergedAt: &merged},
	}

	d := buildIssueDocument("main-org", "acme/widgets", it)
	if d.Kind != "pr" {
		t.Fatalf("Kind = %q, want pr", d.Kind)
	}

	got, err := render.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	compareGolden(t, "pr.md", got)
}

func TestBuildIssueDocument_UnmergedPR(t *testing.T) {
	it := apiIssue{
		Number:      8,
		Title:       "WIP",
		State:       "open",
		HTMLURL:     "https://github.com/acme/widgets/pull/8",
		UpdatedAt:   time.Now(),
		User:        apiUser{Login: "hubot"},
		PullRequest: &apiPullRequestStub{MergedAt: nil},
	}
	d := buildIssueDocument("main-org", "acme/widgets", it)
	if merged, ok := d.Frontmatter["merged"].(bool); !ok || merged {
		t.Fatalf("Frontmatter[merged] = %v, want false", d.Frontmatter["merged"])
	}
}

func TestLabelNames_SortedAndNilWhenEmpty(t *testing.T) {
	if got := labelNames(nil); got != nil {
		t.Fatalf("labelNames(nil) = %v, want nil", got)
	}
	got := labelNames([]apiLabel{{Name: "zeta"}, {Name: "alpha"}})
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("labelNames = %v, want sorted [alpha zeta]", got)
	}
}
