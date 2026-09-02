package sqlite

import (
	"context"
	"testing"
)

func TestAskCacheStorePutGet(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := NewAskCacheStore(db)

	if _, ok, err := s.Get(ctx, "missing"); err != nil || ok {
		t.Fatalf("Get(missing) = (%v, %v), want (false, nil)", ok, err)
	}

	if err := s.Put(ctx, "k", 3, []byte("payload")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	raw, ok, err := s.Get(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("Get(k) = (%v, %v, %v), want hit", string(raw), ok, err)
	}
	if string(raw) != "payload" {
		t.Fatalf("Get(k) = %q, want %q", string(raw), "payload")
	}

	if err := s.Put(ctx, "k", 3, []byte("updated")); err != nil {
		t.Fatalf("Put upsert: %v", err)
	}
	raw, _, _ = s.Get(ctx, "k")
	if string(raw) != "updated" {
		t.Fatalf("Get(k) after upsert = %q, want updated", string(raw))
	}
}

func TestAskCacheStoreInvalidate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := NewAskCacheStore(db)

	for _, k := range []string{"a", "b"} {
		if err := s.Put(ctx, k, 1, []byte(k)); err != nil {
			t.Fatalf("Put(%s): %v", k, err)
		}
	}
	if err := s.Invalidate(ctx); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	for _, k := range []string{"a", "b"} {
		if _, ok, err := s.Get(ctx, k); err != nil || ok {
			t.Fatalf("Get(%s) after invalidate = (%v, %v), want miss", k, ok, err)
		}
	}
}

func TestAskCacheStoreDeleteStale(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := NewAskCacheStore(db)

	if err := s.Put(ctx, "old", 1, []byte("old")); err != nil {
		t.Fatalf("Put(old): %v", err)
	}
	if err := s.Put(ctx, "current", 2, []byte("current")); err != nil {
		t.Fatalf("Put(current): %v", err)
	}
	if err := s.DeleteStale(ctx, 2); err != nil {
		t.Fatalf("DeleteStale: %v", err)
	}
	if _, ok, err := s.Get(ctx, "old"); err != nil || ok {
		t.Fatalf("Get(old) after prune = (%v, %v), want miss", ok, err)
	}
	if raw, ok, err := s.Get(ctx, "current"); err != nil || !ok || string(raw) != "current" {
		t.Fatalf("Get(current) after prune = (%q, %v, %v), want hit", raw, ok, err)
	}
}
