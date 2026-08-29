package state

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestOpenTombstoneStoreNonexistentFileReturnsEmpty(t *testing.T) {
	s, err := OpenTombstoneStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("OpenTombstoneStore: %v", err)
	}
	if s.Contains("github:foo", "123") {
		t.Fatal("Contains() = true, want false")
	}
}

func TestTombstoneAddAndContains(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tombstones.json")
	s, err := OpenTombstoneStore(path)
	if err != nil {
		t.Fatalf("OpenTombstoneStore: %v", err)
	}

	if err := s.Add("github:foo", "issue-1"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !s.Contains("github:foo", "issue-1") {
		t.Fatal("Contains() = false, want true")
	}
	if s.Contains("github:foo", "issue-2") {
		t.Fatal("Contains() for unadded id = true, want false")
	}
	if s.Contains("gitlab:foo", "issue-1") {
		t.Fatal("Contains() leaked across source keys")
	}
}

func TestTombstoneRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tombstones.json")
	s, err := OpenTombstoneStore(path)
	if err != nil {
		t.Fatalf("OpenTombstoneStore: %v", err)
	}

	if err := s.Add("github:foo", "issue-1"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Remove("github:foo", "issue-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.Contains("github:foo", "issue-1") {
		t.Fatal("Contains() after Remove = true, want false")
	}

	if err := s.Remove("github:foo", "never-added"); err != nil {
		t.Fatalf("Remove non-existent: %v", err)
	}
	if err := s.Remove("unknown:source", "never-added"); err != nil {
		t.Fatalf("Remove unknown source: %v", err)
	}
}

func TestTombstonePersistReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tombstones.json")
	s1, err := OpenTombstoneStore(path)
	if err != nil {
		t.Fatalf("OpenTombstoneStore: %v", err)
	}
	if err := s1.Add("wiki:docs", "page-1"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	s2, err := OpenTombstoneStore(path)
	if err != nil {
		t.Fatalf("second OpenTombstoneStore: %v", err)
	}
	if !s2.Contains("wiki:docs", "page-1") {
		t.Fatal("reloaded Contains() = false, want true")
	}
}

func TestTombstoneConcurrentAdd(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tombstones.json")
	s, err := OpenTombstoneStore(path)
	if err != nil {
		t.Fatalf("OpenTombstoneStore: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Add("github:foo", fmt.Sprintf("id-%d", i)); err != nil {
				t.Errorf("Add: %v", err)
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < 20; i++ {
		if !s.Contains("github:foo", fmt.Sprintf("id-%d", i)) {
			t.Fatalf("Contains(id-%d) = false, want true", i)
		}
	}
}
