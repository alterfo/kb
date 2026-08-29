package retriever

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
)

func supersessionCorpus() ([]vector.Chunk, func(string) []float32) {
	chunks := []vector.Chunk{
		{ID: "old", RefDocID: "doc-old", Text: "pricing policy", FileName: "old.md", FilePath: "notes/old.md", Source: "notes", Embedding: []float32{1, 0}, SupersededBy: "doc-new"},
		{ID: "new", RefDocID: "doc-new", Text: "pricing policy update", FileName: "new.md", FilePath: "notes/new.md", Source: "notes", Embedding: []float32{1, 0}},
	}
	return chunks, constVec([]float32{1, 0})
}

func TestStrictSupersedeModeDropsOldWhenNewerPresent(t *testing.T) {
	chunks, vec := supersessionCorpus()
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs, BM25: idx, Hybrid: true,
		Embed:         fakeEmbedder{vec: vec},
		SupersedeMode: SupersedeStrict,
	})

	got, err := r.Retrieve(context.Background(), "pricing policy", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.RefDocID != "doc-new" {
		t.Fatalf("strict mode got %+v, want only doc-new", got)
	}
}

func TestSoftSupersedeModeKeepsBoth(t *testing.T) {
	chunks, vec := supersessionCorpus()
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{Vector: vs, BM25: idx, Hybrid: true, Embed: fakeEmbedder{vec: vec}})

	got, err := r.Retrieve(context.Background(), "pricing policy", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("soft mode got %d chunks, want both versions", len(got))
	}
	for _, sc := range got {
		if sc.Chunk.RefDocID == "doc-old" && sc.Chunk.SupersededBy != "doc-new" {
			t.Errorf("old version lost its superseded marker: %+v", sc.Chunk)
		}
	}
}

func TestStrictSupersedeKeepsOldWithoutNewer(t *testing.T) {
	chunks, vec := supersessionCorpus()
	chunks = chunks[:1]
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{
		Vector: vs, BM25: idx, Hybrid: true,
		Embed:         fakeEmbedder{vec: vec},
		SupersedeMode: SupersedeStrict,
	})

	got, err := r.Retrieve(context.Background(), "pricing policy", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.RefDocID != "doc-old" {
		t.Fatalf("strict mode must keep old version when no newer exists, got %+v", got)
	}
}

func TestSupersedeModeDefaultsToSoft(t *testing.T) {
	chunks, vec := supersessionCorpus()
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	r := New(Config{Vector: vs, BM25: idx, Hybrid: true, Embed: fakeEmbedder{vec: vec}, SupersedeMode: "bogus"})
	got, _ := r.Retrieve(context.Background(), "pricing policy", Options{K: 10})
	if len(got) != 2 {
		t.Fatalf("unknown mode must fall back to soft (both kept), got %d", len(got))
	}
}
