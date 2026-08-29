package bm25

import (
	"reflect"
	"testing"

	"github.com/alterfo/kb/internal/store/vector"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Hello, World!", []string{"hello", "world"}},
		{"Привет, Мир!", []string{"привет", "мир"}},
		{"go1.26 test-case", []string{"go1", "26", "test", "case"}},
		{"", nil},
		{"   ", nil},
	}
	for _, c := range cases {
		got := Tokenize(c.in)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokenize(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestIndex_NeedsRebuild(t *testing.T) {
	idx := New()
	if !idx.NeedsRebuild(1) {
		t.Fatal("fresh index should need a rebuild")
	}
	idx.Rebuild(nil, 1)
	if idx.NeedsRebuild(1) {
		t.Fatal("index should not need a rebuild after Rebuild with the same version")
	}
	if !idx.NeedsRebuild(2) {
		t.Fatal("index should need a rebuild when corpus version changes")
	}
}

func TestIndex_SearchEmptyIndex(t *testing.T) {
	idx := New()
	if got := idx.Search("anything", 10); got != nil {
		t.Errorf("expected nil results on unbuilt index, got %#v", got)
	}
	idx.Rebuild(nil, 1)
	if got := idx.Search("anything", 10); got != nil {
		t.Errorf("expected nil results on empty corpus, got %#v", got)
	}
}

func TestIndex_SearchRanking(t *testing.T) {
	idx := New()
	chunks := []vector.Chunk{
		{ID: "a", Text: "the quick brown fox jumps over the lazy dog"},
		{ID: "b", Text: "fox fox fox fox everywhere in this document about foxes"},
		{ID: "c", Text: "an entirely unrelated document about cats and dogs"},
	}
	idx.Rebuild(chunks, 1)

	results := idx.Search("fox", 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 docs to match 'fox', got %d: %#v", len(results), results)
	}
	if results[0].ID != "b" {
		t.Errorf("expected doc 'b' (more fox occurrences) to rank first, got %q", results[0].ID)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("expected doc b score > doc a score, got %v <= %v", results[0].Score, results[1].Score)
	}
}

func TestIndex_SearchTopK(t *testing.T) {
	idx := New()
	chunks := []vector.Chunk{
		{ID: "a", Text: "apple banana cherry"},
		{ID: "b", Text: "apple banana"},
		{ID: "c", Text: "apple"},
	}
	idx.Rebuild(chunks, 1)

	results := idx.Search("apple banana cherry", 2)
	if len(results) != 2 {
		t.Fatalf("expected top-2 results, got %d", len(results))
	}
	if results[0].ID != "a" {
		t.Errorf("expected 'a' to rank first, got %q", results[0].ID)
	}
}

func TestIndex_SearchNoMatch(t *testing.T) {
	idx := New()
	idx.Rebuild([]vector.Chunk{{ID: "a", Text: "hello world"}}, 1)
	if got := idx.Search("nonexistent", 10); len(got) != 0 {
		t.Errorf("expected no results, got %#v", got)
	}
}

func TestIndex_CyrillicRanking(t *testing.T) {
	idx := New()
	chunks := []vector.Chunk{
		{ID: "a", Text: "кот сидит на окне"},
		{ID: "b", Text: "собака бежит по улице"},
	}
	idx.Rebuild(chunks, 1)

	results := idx.Search("кот", 10)
	if len(results) != 1 || results[0].ID != "a" {
		t.Fatalf("expected only doc 'a' to match 'кот', got %#v", results)
	}
}

func TestIndex_InvalidationAfterWrite(t *testing.T) {
	idx := New()
	idx.Rebuild([]vector.Chunk{{ID: "a", Text: "old content about apples"}}, 1)

	results := idx.Search("apples", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result before update, got %d", len(results))
	}

	if !idx.NeedsRebuild(2) {
		t.Fatal("expected index to be stale after corpus_version bump")
	}

	idx.Rebuild([]vector.Chunk{{ID: "b", Text: "new content about oranges"}}, 2)

	if idx.NeedsRebuild(2) {
		t.Fatal("index should be up to date after rebuild")
	}
	if got := idx.Search("apples", 10); len(got) != 0 {
		t.Errorf("expected stale term 'apples' to be gone after rebuild, got %#v", got)
	}
	if got := idx.Search("oranges", 10); len(got) != 1 {
		t.Errorf("expected new content to be searchable after rebuild, got %#v", got)
	}
}

func TestIndex_SearchZeroK(t *testing.T) {
	idx := New()
	idx.Rebuild([]vector.Chunk{{ID: "a", Text: "hello"}}, 1)
	if got := idx.Search("hello", 0); got != nil {
		t.Errorf("expected nil for k<=0, got %#v", got)
	}
}
