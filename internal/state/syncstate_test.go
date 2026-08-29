package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStoreNonexistentFileReturnsEmpty(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if cur := s.Cursor("github:foo"); cur != "" {
		t.Fatalf("Cursor() = %q, want empty", cur)
	}
	if _, ok := s.Get("github:foo"); ok {
		t.Fatal("Get() ok = true, want false")
	}
}

func TestAdvancePersistsCursorAndSyncTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync-state.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := s.Advance("github:foo", "cursor-1", now); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	if cur := s.Cursor("github:foo"); cur != "cursor-1" {
		t.Fatalf("Cursor() = %q, want cursor-1", cur)
	}
	st, ok := s.Get("github:foo")
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if !st.LastSyncAt.Equal(now) {
		t.Fatalf("LastSyncAt = %v, want %v", st.LastSyncAt, now)
	}
	if st.LastError != "" {
		t.Fatalf("LastError = %q, want empty", st.LastError)
	}
}

func TestRecordErrorPreservesCursorRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync-state.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Advance("gitlab:bar", "cursor-1", t1); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	t2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := s.RecordError("gitlab:bar", t2, "boom"); err != nil {
		t.Fatalf("RecordError: %v", err)
	}

	if cur := s.Cursor("gitlab:bar"); cur != "cursor-1" {
		t.Fatalf("Cursor() after RecordError = %q, want cursor-1 (rollback)", cur)
	}
	st, ok := s.Get("gitlab:bar")
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if st.LastError != "boom" {
		t.Fatalf("LastError = %q, want boom", st.LastError)
	}
	if !st.LastSyncAt.Equal(t2) {
		t.Fatalf("LastSyncAt = %v, want %v", st.LastSyncAt, t2)
	}
}

func TestOpenStoreReloadsPersistedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync-state.json")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s1.Advance("wiki:docs", "cursor-x", now); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := s1.RecordError("wiki:docs", now.Add(time.Hour), "boom"); err != nil {
		t.Fatalf("RecordError: %v", err)
	}

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("second OpenStore: %v", err)
	}
	if cur := s2.Cursor("wiki:docs"); cur != "cursor-x" {
		t.Fatalf("reloaded Cursor() = %q, want cursor-x", cur)
	}
	st, ok := s2.Get("wiki:docs")
	if !ok {
		t.Fatal("reloaded Get() ok = false, want true")
	}
	if st.LastError != "boom" {
		t.Fatalf("reloaded LastError = %q, want boom", st.LastError)
	}
	if !st.LastSyncAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("reloaded LastSyncAt = %v, want %v", st.LastSyncAt, now.Add(time.Hour))
	}
}

func TestOpenStoreEmptyFileReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync-state.json")
	if err := atomicWriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seeding empty file: %v", err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if cur := s.Cursor("any:thing"); cur != "" {
		t.Fatalf("Cursor() = %q, want empty", cur)
	}
}

func TestAdvanceConcurrentKeysNoRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync-state.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		i := i
		go func() {
			key := filepath.Join("source", string(rune('a'+i)))
			done <- s.Advance(key, "cursor", time.Now())
		}()
	}
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent Advance: %v", err)
		}
	}
}
