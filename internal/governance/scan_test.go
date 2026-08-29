package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func chtimes(t *testing.T, root, rel string, mtime time.Time) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.Chtimes(full, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", rel, err)
	}
}

func TestScanDuplicatesKeepsNewest(t *testing.T) {
	root := t.TempDir()
	body := "same body text here, long enough to not be treated as a near-empty document by the scanner"
	writeFile(t, root, "notes/old.md", "---\nid: a\n---\n\n"+body)
	writeFile(t, root, "notes/new.md", "---\nid: b\n---\n\n"+body)
	now := time.Now()
	chtimes(t, root, "notes/old.md", now.Add(-time.Hour))
	chtimes(t, root, "notes/new.md", now)

	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(plan.Duplicates) != 1 {
		t.Fatalf("Duplicates = %+v, want 1 group", plan.Duplicates)
	}
	g := plan.Duplicates[0]
	if g.Keep.Path != "notes/new.md" {
		t.Fatalf("Keep = %q, want notes/new.md", g.Keep.Path)
	}
	if len(g.Trash) != 1 || g.Trash[0].Path != "notes/old.md" {
		t.Fatalf("Trash = %+v, want [notes/old.md]", g.Trash)
	}
}

func TestScanEmptyDocuments(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/short.md", "---\nid: a\n---\n\ntiny")
	writeFile(t, root, "notes/long.md", "---\nid: b\n---\n\n"+strings.Repeat("x", EmptyBodyMaxChars+1))

	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(plan.Empty) != 1 || plan.Empty[0].Path != "notes/short.md" {
		t.Fatalf("Empty = %+v, want [notes/short.md]", plan.Empty)
	}
}

func TestScanMergeByNormalizedStem(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/onboarding-plan.md", "---\nid: a\n---\n\nfirst version of the plan")
	writeFile(t, root, "notes/onboarding-plan-2.md", "---\nid: b\n---\n\nsecond version, different text")
	writeFile(t, root, "notes/unrelated.md", "---\nid: c\n---\n\nsomething else entirely")

	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(plan.Merge) != 1 {
		t.Fatalf("Merge = %+v, want 1 group", plan.Merge)
	}
	want := []string{"notes/onboarding-plan-2.md", "notes/onboarding-plan.md"}
	got := plan.Merge[0].Paths
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Merge[0].Paths = %v, want %v", got, want)
	}
}

func TestScanMergeExcludesNonRewriteSources(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "github/plan.md", "---\nid: a\n---\n\nfirst version")
	writeFile(t, root, "github/plan-2.md", "---\nid: b\n---\n\nsecond version")

	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(plan.Merge) != 0 {
		t.Fatalf("Merge = %+v, want none (github is not a rewrite source)", plan.Merge)
	}
}

func TestScanCompressCandidates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/big.md", "---\nid: a\n---\n\n"+strings.Repeat("x", CompressMinBytes+1))
	writeFile(t, root, "notes/small.md", "---\nid: b\n---\n\nsmall body")

	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(plan.Compress) != 1 || plan.Compress[0].Path != "notes/big.md" {
		t.Fatalf("Compress = %+v, want [notes/big.md]", plan.Compress)
	}
}

func TestScanCompressExcludesMerged(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("x", CompressMinBytes+1)
	writeFile(t, root, "notes/plan.md", "---\nid: a\n---\n\n"+big+" version one")
	writeFile(t, root, "notes/plan-2.md", "---\nid: b\n---\n\n"+big+" version two")

	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(plan.Merge) != 1 {
		t.Fatalf("Merge = %+v, want 1 group", plan.Merge)
	}
	if len(plan.Compress) != 0 {
		t.Fatalf("Compress = %+v, want none — merge already rewrites these", plan.Compress)
	}
}

func TestScanExcludesReportsFromDeleteDetection(t *testing.T) {
	root := t.TempDir()
	body := "same body text here, long enough to not be treated as a near-empty document by the scanner"
	writeFile(t, root, "reports/r1.md", "---\nid: a\n---\n\n"+body)
	writeFile(t, root, "reports/r2.md", "---\nid: b\n---\n\n"+body)

	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(plan.Duplicates) != 0 {
		t.Fatalf("Duplicates = %+v, want none — reports is excluded from delete detection", plan.Duplicates)
	}
}

func TestScanSkipsHiddenDirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "notes/a.md", "---\nid: a\n---\n\nkeep")
	writeFile(t, root, ".trash/notes/a.md", "---\nid: a\n---\n\nkeep")

	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(plan.Duplicates) != 0 {
		t.Fatalf("Duplicates = %+v, want none — .trash is skipped", plan.Duplicates)
	}
}

func TestScanEmptyRootDoesNotError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(plan.Duplicates)+len(plan.Empty)+len(plan.Merge)+len(plan.Compress) != 0 {
		t.Fatalf("plan = %+v, want empty", plan)
	}
}

func TestPlanCounts(t *testing.T) {
	root := t.TempDir()
	body := "same body text here, long enough to not be treated as a near-empty document by the scanner"
	writeFile(t, root, "notes/dup1.md", "---\nid: a\n---\n\n"+body)
	writeFile(t, root, "notes/dup2.md", "---\nid: b\n---\n\n"+body)
	writeFile(t, root, "notes/empty.md", "---\nid: c\n---\n\ntiny")

	plan, err := Scan(root, DefaultScanOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	dup, empty, merge, compress := plan.Counts()
	if dup != 1 || empty != 1 || merge != 0 || compress != 0 {
		t.Fatalf("Counts = %d,%d,%d,%d, want 1,1,0,0", dup, empty, merge, compress)
	}
}
