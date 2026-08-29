package got

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestRetrieveRetriesThenFailsOpen(t *testing.T) {
	calls := 0
	var sleeps []time.Duration
	cfg := baseConfig()
	cfg.MaxRetries = 2
	cfg.BaseDelay = 10 * time.Millisecond
	cfg.MaxDelay = 100 * time.Millisecond
	cfg.JitterFunc = func() float64 { return 1 }
	cfg.Sleep = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}
	cfg.Retriever = fakeRetriever{err: errors.New("boom"), calls: &calls}
	o := New(cfg)

	got := o.retrieve(context.Background(), "q", retriever.ModeLocal, vector.Filter{})
	if got != nil {
		t.Fatalf("retrieve = %v, want nil after exhausted retries", got)
	}
	if calls != 3 {
		t.Fatalf("retriever calls = %d, want 3 (1 + MaxRetries)", calls)
	}
	if len(sleeps) != 2 {
		t.Fatalf("sleeps = %v, want 2 backoff sleeps", sleeps)
	}
	for i, d := range sleeps {
		if d > cfg.MaxDelay || d <= 0 {
			t.Fatalf("sleep[%d] = %v, want within (0, MaxDelay]", i, d)
		}
	}
}

func TestRetrieveSucceedsOnRetry(t *testing.T) {
	calls := 0
	cfg := baseConfig()
	cfg.MaxRetries = 3
	cfg.JitterFunc = func() float64 { return 1 }
	cfg.Sleep = func(_ context.Context, _ time.Duration) error { return nil }
	cfg.Retriever = flakyRetriever{failFirst: 1, calls: &calls}
	o := New(cfg)

	got := o.retrieve(context.Background(), "q", retriever.ModeLocal, vector.Filter{})
	if len(got) != 1 || got[0].ID != "ok" {
		t.Fatalf("retrieve = %+v, want [ok]", got)
	}
	if calls != 2 {
		t.Fatalf("retriever calls = %d, want 2 (fail once, succeed once)", calls)
	}
}

func TestChatRetriesThenFailsOpen(t *testing.T) {
	calls := 0
	cfg := baseConfig()
	cfg.MaxRetries = 2
	cfg.Sleep = func(_ context.Context, _ time.Duration) error { return nil }
	cfg.Chat = fakeChat{err: errors.New("boom"), calls: &calls}
	o := New(cfg)

	resp, ok := o.chat(context.Background(), llm.ChatRequest{})
	if ok {
		t.Fatalf("chat ok = true, want fail-open false")
	}
	if resp.Content != "" {
		t.Fatalf("chat content = %q, want empty", resp.Content)
	}
	if calls != 3 {
		t.Fatalf("chat calls = %d, want 3 (1 + MaxRetries)", calls)
	}
}

func TestWaitAbortsRetriesOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	cfg := baseConfig()
	cfg.MaxRetries = 5
	cfg.Sleep = func(_ context.Context, _ time.Duration) error { return ctx.Err() }
	cfg.Retriever = fakeRetriever{err: errors.New("boom"), calls: &calls}
	o := New(cfg)

	got := o.retrieve(ctx, "q", retriever.ModeLocal, vector.Filter{})
	if got != nil {
		t.Fatalf("retrieve = %v, want nil", got)
	}
	if calls != 1 {
		t.Fatalf("retriever calls = %d, want 1 (no retry after canceled sleep)", calls)
	}
}

func TestBackoffDelayCappedAndOverflowSafe(t *testing.T) {
	cfg := baseConfig()
	cfg.BaseDelay = 10 * time.Millisecond
	cfg.MaxDelay = 200 * time.Millisecond
	cfg.JitterFunc = func() float64 { return 1 }
	o := New(cfg)

	if d := o.backoffDelay(5); d != 200*time.Millisecond {
		t.Fatalf("backoffDelay(5) = %v, want MaxDelay cap", d)
	}
	if d := o.backoffDelay(100); d != 200*time.Millisecond {
		t.Fatalf("backoffDelay(100) = %v, want MaxDelay (shift overflow guard)", d)
	}
}

type flakyRetriever struct {
	failFirst int
	calls     *int
}

func (f flakyRetriever) Retrieve(ctx context.Context, query string, k int) ([]vector.ScoredChunk, error) {
	if f.calls != nil {
		*f.calls++
	}
	if *f.calls <= f.failFirst {
		return nil, errors.New("transient")
	}
	return []vector.ScoredChunk{{Chunk: vector.Chunk{ID: "ok"}}}, nil
}

func (f flakyRetriever) RetrieveMode(ctx context.Context, query string, k int, mode retriever.Mode) ([]vector.ScoredChunk, error) {
	return f.Retrieve(ctx, query, k)
}
