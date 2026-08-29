package sink

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

type fakeSink struct {
	name       string
	writeErr   error
	pruneErr   error
	tombErr    error
	writes     []connector.Document
	pruneCalls int
	tombCalls  int
}

func (f *fakeSink) Write(ctx context.Context, d connector.Document) error {
	f.writes = append(f.writes, d)
	return f.writeErr
}

func (f *fakeSink) Prune(ctx context.Context, sourceName string, seen map[string]struct{}, prefixes ...string) error {
	f.pruneCalls++
	return f.pruneErr
}

func (f *fakeSink) Tombstone(ctx context.Context, sourceName, id string) error {
	f.tombCalls++
	return f.tombErr
}

func TestTeeSinkWriteCallsAllSinks(t *testing.T) {
	a := &fakeSink{}
	b := &fakeSink{}
	tee := NewTeeSink(a, b)

	d := connector.Document{ID: "1", Source: "s"}
	if err := tee.Write(context.Background(), d); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(a.writes) != 1 || len(b.writes) != 1 {
		t.Fatalf("expected both sinks to receive the write, got a=%d b=%d", len(a.writes), len(b.writes))
	}
}

func TestTeeSinkWriteAggregatesErrorsWithoutStopping(t *testing.T) {
	failErr := errors.New("boom")
	a := &fakeSink{writeErr: failErr}
	b := &fakeSink{}
	tee := NewTeeSink(a, b)

	err := tee.Write(context.Background(), connector.Document{ID: "1", Source: "s"})
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	if !errors.Is(err, failErr) {
		t.Fatalf("expected errors.Is to find failErr, got %v", err)
	}
	if len(b.writes) != 1 {
		t.Fatal("expected second sink to still be called despite first sink's error")
	}
}

func TestTeeSinkPruneCallsAllSinks(t *testing.T) {
	a := &fakeSink{}
	b := &fakeSink{}
	tee := NewTeeSink(a, b)

	if err := tee.Prune(context.Background(), "s", map[string]struct{}{}); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if a.pruneCalls != 1 || b.pruneCalls != 1 {
		t.Fatalf("expected both sinks pruned, got a=%d b=%d", a.pruneCalls, b.pruneCalls)
	}
}

func TestTeeSinkTombstoneCallsAllSinks(t *testing.T) {
	a := &fakeSink{}
	b := &fakeSink{}
	tee := NewTeeSink(a, b)

	if err := tee.Tombstone(context.Background(), "s", "1"); err != nil {
		t.Fatalf("Tombstone: %v", err)
	}
	if a.tombCalls != 1 || b.tombCalls != 1 {
		t.Fatalf("expected both sinks tombstoned, got a=%d b=%d", a.tombCalls, b.tombCalls)
	}
}
