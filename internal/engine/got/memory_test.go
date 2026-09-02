package got

import (
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/store/vector"
)

func TestRollingMemoryWindowShift(t *testing.T) {
	m := newRollingMemory(3)
	m.add(subgoalResult{ID: "0", Query: "q0", Answer: "a0"})
	m.add(subgoalResult{ID: "1", Query: "q1", Answer: "a1"})
	m.add(subgoalResult{ID: "2", Query: "q2", Answer: "a2"})
	m.add(subgoalResult{ID: "3", Query: "q3", Answer: "a3"})

	got := m.snapshot()
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	for i, want := range []string{"a1", "a2", "a3"} {
		if got[i].Answer != want {
			t.Fatalf("entry %d answer = %q, want %q (oldest shifted out)", i, got[i].Answer, want)
		}
	}
}

func TestRollingMemorySizeZeroRetainsNothing(t *testing.T) {
	m := newRollingMemory(0)
	m.add(subgoalResult{ID: "0", Query: "q0", Answer: "a0"})
	if got := m.snapshot(); got != nil {
		t.Fatalf("got %+v, want nil for zero window", got)
	}
}

func TestRollingMemorySnapshotIsCopy(t *testing.T) {
	m := newRollingMemory(2)
	m.add(subgoalResult{ID: "0", Query: "q0", Answer: "a0"})
	got := m.snapshot()
	got[0].Answer = "mutated"
	if m.snapshot()[0].Answer != "a0" {
		t.Fatalf("snapshot must not alias internal entries: %+v", m.snapshot())
	}
}

func TestRollingMemoryDropsDependentInjections(t *testing.T) {
	m := newRollingMemory(3)
	m.add(subgoalResult{ID: "dep", Query: "dep", Answer: "dep answer", Deps: []string{"0"}})
	if got := m.snapshot(); got != nil {
		t.Fatalf("dependent injection leaked into rolling memory: %+v", got)
	}

	m.add(subgoalResult{ID: "root", Query: "root", Answer: "root answer"})
	got := m.snapshot()
	if len(got) != 1 || got[0].ID != "root" {
		t.Fatalf("got %+v, want only the independent subgoal in memory", got)
	}
}

func TestMergeRollingMemoryPinsDepsBeforeOthers(t *testing.T) {
	deps := []subgoalResult{{ID: "dep", Query: "dependency", Answer: "dep answer"}}
	memory := []subgoalResult{
		{ID: "old", Query: "old", Answer: "old answer"},
		{ID: "dep", Query: "dependency", Answer: "dep answer"},
		{ID: "new", Query: "new", Answer: "new answer"},
	}
	got := mergeRollingMemory(deps, memory)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (dep pinned, no duplicate): %+v", len(got), got)
	}
	if got[0].ID != "dep" {
		t.Fatalf("first entry = %q, want pinned dep", got[0].ID)
	}
	if got[1].ID != "old" || got[2].ID != "new" {
		t.Fatalf("window entries out of order: %+v", got)
	}
}

func TestBuildSynthesizePromptEmptyWindowMatchesLegacy(t *testing.T) {
	chunks := []vector.ScoredChunk{{Chunk: vector.Chunk{FileName: "a.md", Text: "fact"}}}
	got := buildSynthesizePrompt("q", chunks, nil, nil)
	want := "Sub-question: q\n\nExcerpts:\n- (a.md) fact\n"
	if got != want {
		t.Fatalf("prompt changed with empty memory:\n got %q\nwant %q", got, want)
	}
}

func TestBuildSynthesizePromptRollingWindowGolden(t *testing.T) {
	chunks := []vector.ScoredChunk{{Chunk: vector.Chunk{FileName: "a.md", Text: "fact"}}}
	deps := []subgoalResult{{ID: "1", Query: "q1", Answer: "a1"}}
	memory := []subgoalResult{
		{ID: "2", Query: "q2", Answer: "a2"},
		{ID: "3", Query: "q3", Answer: "a3"},
	}
	got := buildSynthesizePrompt("q", chunks, deps, memory)
	want := "Previously resolved sub-answers:\n- q1: a1\n- q2: a2\n- q3: a3\n\nSub-question: q\n\nExcerpts:\n- (a.md) fact\n"
	if got != want {
		t.Fatalf("rolling memory prompt mismatch:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(got, "Previously resolved sub-answers:") {
		t.Fatalf("prompt missing rolling memory section:\n%s", got)
	}
}

func TestFormatRollingMemoryContextEmpty(t *testing.T) {
	if got := formatRollingMemoryContext(nil, nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := formatRollingMemoryContext(nil, []subgoalResult{}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestNewDefaultsRollingMemory(t *testing.T) {
	o := New(Config{})
	if o.cfg.RollingMemory != 3 {
		t.Fatalf("got RollingMemory %d, want 3", o.cfg.RollingMemory)
	}
}
