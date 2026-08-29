package dragon

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
)

func TestRunQuestions_SequentialResults(t *testing.T) {
	qs := []corpus.Question{
		{ID: "0", Text: "q0"},
		{ID: "1", Text: "q1"},
		{ID: "2", Text: "q2"},
	}
	ask := func(ctx context.Context, q corpus.Question) (string, []string) {
		return "answer-" + q.ID, []string{"doc-" + q.ID}
	}

	entries := RunQuestions(context.Background(), qs, 1, ask, nil)
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	for _, id := range []string{"0", "1", "2"} {
		e, ok := entries[id]
		if !ok {
			t.Fatalf("missing entry for id %q", id)
		}
		if e.ModelAnswer != "answer-"+id {
			t.Errorf("entries[%q].ModelAnswer = %q", id, e.ModelAnswer)
		}
		if len(e.FoundIDs) != 1 || e.FoundIDs[0] != "doc-"+id {
			t.Errorf("entries[%q].FoundIDs = %v", id, e.FoundIDs)
		}
	}
}

func TestRunQuestions_ConcurrentIsRaceFree(t *testing.T) {
	qs := make([]corpus.Question, 50)
	for i := range qs {
		qs[i] = corpus.Question{ID: fmt.Sprintf("%d", i), Text: fmt.Sprintf("q%d", i)}
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	ask := func(ctx context.Context, q corpus.Question) (string, []string) {
		mu.Lock()
		seen[q.ID] = true
		mu.Unlock()
		return "a-" + q.ID, nil
	}

	entries := RunQuestions(context.Background(), qs, 8, ask, nil)
	if len(entries) != len(qs) {
		t.Fatalf("len(entries) = %d, want %d", len(entries), len(qs))
	}
	if len(seen) != len(qs) {
		t.Fatalf("len(seen) = %d, want %d", len(seen), len(qs))
	}
}

func TestRunQuestions_ZeroConcurrencyDefaultsToOne(t *testing.T) {
	qs := []corpus.Question{{ID: "0", Text: "q0"}}
	ask := func(ctx context.Context, q corpus.Question) (string, []string) {
		return "ok", nil
	}
	entries := RunQuestions(context.Background(), qs, 0, ask, nil)
	if entries["0"].ModelAnswer != "ok" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestRunQuestions_Empty(t *testing.T) {
	entries := RunQuestions(context.Background(), nil, 4, func(ctx context.Context, q corpus.Question) (string, []string) {
		t.Fatal("ask should not be called")
		return "", nil
	}, nil)
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want empty", entries)
	}
}

func TestRunQuestions_ProgressCallback(t *testing.T) {
	qs := make([]corpus.Question, 20)
	for i := range qs {
		qs[i] = corpus.Question{ID: fmt.Sprintf("%d", i), Text: fmt.Sprintf("q%d", i)}
	}
	ask := func(ctx context.Context, q corpus.Question) (string, []string) {
		return "a-" + q.ID, nil
	}
	var mu sync.Mutex
	var calls []int
	onProgress := func(done int) {
		mu.Lock()
		calls = append(calls, done)
		mu.Unlock()
	}

	RunQuestions(context.Background(), qs, 4, ask, onProgress)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != len(qs) {
		t.Fatalf("onProgress called %d times, want %d", len(calls), len(qs))
	}
	max := 0
	for _, c := range calls {
		if c > max {
			max = c
		}
	}
	if max != len(qs) {
		t.Fatalf("max progress value = %d, want %d", max, len(qs))
	}
}

func TestRunQuestions_NilProgressCallbackIsSafe(t *testing.T) {
	qs := []corpus.Question{{ID: "0", Text: "q0"}}
	ask := func(ctx context.Context, q corpus.Question) (string, []string) {
		return "ok", nil
	}
	entries := RunQuestions(context.Background(), qs, 1, ask, nil)
	if entries["0"].ModelAnswer != "ok" {
		t.Fatalf("entries = %+v", entries)
	}
}
