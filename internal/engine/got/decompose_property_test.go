package got

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

func TestCleanSubgoalItemsRemapsDependsOn(t *testing.T) {
	items := []decomposeItem{
		{Subquestion: "q0", DependsOn: []string{"1", "2"}},
		{Subquestion: "   "},
		{Subquestion: "q2", DependsOn: []string{"0", "1", "2", "9", "-1", "bad", "0"}},
	}

	got := cleanSubgoalItems(items)
	want := []subgoalSpec{
		{Query: "q0", DependsOn: []string{"1"}},
		{Query: "q2", DependsOn: []string{"0"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanSubgoalItems = %#v, want %#v", got, want)
	}
}

func TestCleanSubgoalItemsPropertyNoDanglingAndIdempotent(t *testing.T) {
	rng := rand.New(rand.NewSource(20260902))
	for round := 0; round < 250; round++ {
		items := randomDecomposeItems(rng)
		cleaned := cleanSubgoalItems(items)
		assertNoDanglingSubgoals(t, cleaned)

		second := cleanSubgoalItems(specsToDecomposeItems(cleaned))
		if !reflect.DeepEqual(cleaned, second) {
			t.Fatalf("round %d: cleanup is not idempotent\nfirst:  %#v\nsecond: %#v", round, cleaned, second)
		}
	}
}

func randomDecomposeItems(rng *rand.Rand) []decomposeItem {
	n := rng.Intn(6) + 1
	items := make([]decomposeItem, n)
	for i := range items {
		if rng.Intn(4) == 0 {
			items[i].Subquestion = "   "
		} else {
			items[i].Subquestion = "q" + strconv.Itoa(i)
		}
		depCount := rng.Intn(4)
		for j := 0; j < depCount; j++ {
			idx := rng.Intn(n+2) - 1
			items[i].DependsOn = append(items[i].DependsOn, strconv.Itoa(idx))
		}
	}
	return items
}

func specsToDecomposeItems(specs []subgoalSpec) []decomposeItem {
	items := make([]decomposeItem, len(specs))
	for i, spec := range specs {
		items[i] = decomposeItem{Subquestion: spec.Query, DependsOn: append([]string(nil), spec.DependsOn...)}
	}
	return items
}

func assertNoDanglingSubgoals(t *testing.T, specs []subgoalSpec) {
	t.Helper()
	for i, spec := range specs {
		seen := make(map[int]struct{}, len(spec.DependsOn))
		for _, raw := range spec.DependsOn {
			idx, err := strconv.Atoi(raw)
			if err != nil {
				t.Fatalf("spec %d has unparsable dependency %q", i, raw)
			}
			if idx < 0 || idx >= len(specs) {
				t.Fatalf("spec %d has out-of-range dependency %d", i, idx)
			}
			if idx == i {
				t.Fatalf("spec %d has self dependency %d", i, idx)
			}
			if _, dup := seen[idx]; dup {
				t.Fatalf("spec %d has duplicate dependency %d", i, idx)
			}
			seen[idx] = struct{}{}
		}
	}
}
