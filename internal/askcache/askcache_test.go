package askcache

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/engine/got"
)

type fakeVersioner struct {
	version int
	err     error
}

func (f fakeVersioner) CorpusVersion(context.Context) (int, error) {
	return f.version, f.err
}

type memStore struct {
	entries map[string]entry
	// failGet makes Get return an error to exercise the fail-open path.
	failGet bool
}

type entry struct {
	version int
	value   []byte
}

func newMemStore() *memStore {
	return &memStore{entries: map[string]entry{}}
}

func (m *memStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if m.failGet {
		return nil, false, errors.New("get boom")
	}
	e, ok := m.entries[key]
	if !ok {
		return nil, false, nil
	}
	return e.value, true, nil
}

func (m *memStore) Put(ctx context.Context, key string, version int, value []byte) error {
	m.entries[key] = entry{version: version, value: append([]byte(nil), value...)}
	return nil
}

func (m *memStore) Invalidate(ctx context.Context) error {
	m.entries = map[string]entry{}
	return nil
}

func (m *memStore) DeleteStale(ctx context.Context, version int) error {
	for k, e := range m.entries {
		if e.version != version {
			delete(m.entries, k)
		}
	}
	return nil
}

func TestKeyUniqueAndStable(t *testing.T) {
	if Key("q", 1, "hash") != Key("q", 1, "hash") {
		t.Fatal("Key not stable")
	}
	distinct := []string{
		Key("q", 1, "hash"),
		Key("q", 2, "hash"),
		Key("q", 1, "other"),
		Key("other", 1, "hash"),
	}
	seen := map[string]bool{}
	for _, k := range distinct {
		if seen[k] {
			t.Fatalf("duplicate key %q", k)
		}
		seen[k] = true
	}
	if strings.Contains(Key("q", 1, "hash"), " ") {
		t.Fatal("Key must not contain spaces")
	}
}

func TestCachePutGetRoundTrip(t *testing.T) {
	store := newMemStore()
	c := New(store, fakeVersioner{version: 7}, "config-hash")
	ctx := context.Background()

	want := got.ThoughtGraph{Query: "capital France", FinalAnswer: "Paris", Sources: []got.Source{{FilePath: "notes/doc1.md"}}}
	if err := c.Put(ctx, want.Query, want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	gotGraph, ok, err := c.Get(ctx, want.Query)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get = miss, want hit")
	}
	if gotGraph.FinalAnswer != want.FinalAnswer || gotGraph.Query != want.Query {
		t.Fatalf("Get = %+v, want %+v", gotGraph, want)
	}
	if len(gotGraph.Sources) != 1 || gotGraph.Sources[0].FilePath != "notes/doc1.md" {
		t.Fatalf("Get sources = %+v", gotGraph.Sources)
	}
}

func TestCacheMissOnDifferentCorpusVersion(t *testing.T) {
	store := newMemStore()
	c := New(store, fakeVersioner{version: 7}, "config-hash")
	ctx := context.Background()

	if err := c.Put(ctx, "q", got.ThoughtGraph{Query: "q", FinalAnswer: "v1"}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	c.Versioner = fakeVersioner{version: 8}
	if _, ok, err := c.Get(ctx, "q"); err != nil || ok {
		t.Fatalf("Get after corpus bump = (%v, %v), want miss", ok, err)
	}
}

func TestCacheGetFailOpenOnVersionerError(t *testing.T) {
	store := newMemStore()
	c := New(store, fakeVersioner{err: errors.New("db down")}, "config-hash")
	if _, ok, err := c.Get(context.Background(), "q"); err == nil || ok {
		t.Fatalf("Get = (%v, %v), want (false, error)", ok, err)
	}
	if err := c.Put(context.Background(), "q", got.ThoughtGraph{Query: "q"}); err == nil {
		t.Fatal("Put with versioner error = nil, want error")
	}
}

func TestCacheGetFailOpenOnStoreError(t *testing.T) {
	store := newMemStore()
	store.failGet = true
	c := New(store, fakeVersioner{version: 1}, "config-hash")
	if _, ok, err := c.Get(context.Background(), "q"); err == nil || ok {
		t.Fatalf("Get = (%v, %v), want (false, error)", ok, err)
	}
}

func TestCacheInvalidateClearsAll(t *testing.T) {
	store := newMemStore()
	c := New(store, fakeVersioner{version: 1}, "config-hash")
	ctx := context.Background()
	if err := c.Put(ctx, "a", got.ThoughtGraph{Query: "a", FinalAnswer: "A"}); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := c.Put(ctx, "b", got.ThoughtGraph{Query: "b", FinalAnswer: "B"}); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if err := c.Invalidate(ctx); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, ok, err := c.Get(ctx, "a"); err != nil || ok {
		t.Fatalf("Get a after invalidate = (%v, %v), want miss", ok, err)
	}
}

func TestCachePruneStaleKeepsCurrent(t *testing.T) {
	store := newMemStore()
	c := New(store, fakeVersioner{version: 1}, "config-hash")
	ctx := context.Background()
	if err := c.Put(ctx, "old", got.ThoughtGraph{Query: "old", FinalAnswer: "old"}); err != nil {
		t.Fatalf("Put old: %v", err)
	}
	c.Versioner = fakeVersioner{version: 2}
	if err := c.Put(ctx, "new", got.ThoughtGraph{Query: "new", FinalAnswer: "new"}); err != nil {
		t.Fatalf("Put new: %v", err)
	}
	if err := c.PruneStale(ctx); err != nil {
		t.Fatalf("PruneStale: %v", err)
	}
	if len(store.entries) != 1 {
		t.Fatalf("entries after prune = %d, want 1", len(store.entries))
	}
	if _, ok, err := c.Get(ctx, "new"); err != nil || !ok {
		t.Fatalf("Get new after prune = (%v, %v), want hit", ok, err)
	}
}

func TestCacheSatisfiesGotAskCache(t *testing.T) {
	var _ got.AskCache = (*Cache)(nil)
	var _ Store = (*memStore)(nil)
}
