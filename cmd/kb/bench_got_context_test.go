package main

import (
	"testing"

	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
)

type fakeBM25Searcher struct {
	chunks map[string]vector.Chunk
}

func (f fakeBM25Searcher) Search(query string, k int) []bm25.ScoredID { return nil }

func (f fakeBM25Searcher) Chunk(id string) (vector.Chunk, bool) {
	c, ok := f.chunks[id]
	return c, ok
}

func TestGotContextChunksKeepsMultipleChunksPerDocument(t *testing.T) {
	searcher := fakeBM25Searcher{chunks: map[string]vector.Chunk{
		"chunk-1": {ID: "chunk-1", RefDocID: "doc-a", Text: "first relevant passage"},
		"chunk-2": {ID: "chunk-2", RefDocID: "doc-a", Text: "second relevant passage"},
	}}
	g := got.ThoughtGraph{
		ChunkSources: []got.Source{
			{DocID: "doc-a", ChunkID: "chunk-1"},
			{DocID: "doc-a", ChunkID: "chunk-2"},
		},
	}

	chunks := gotContextChunks(g, searcher)
	if len(chunks) != 2 {
		t.Fatalf("got %d context chunks, want 2 (both chunks from doc-a must survive): %+v", len(chunks), chunks)
	}
	texts := map[string]bool{chunks[0].Text: true, chunks[1].Text: true}
	if !texts["first relevant passage"] || !texts["second relevant passage"] {
		t.Fatalf("missing expected chunk text: %+v", chunks)
	}
}

func TestGotContextChunksDedupesSameChunkID(t *testing.T) {
	searcher := fakeBM25Searcher{chunks: map[string]vector.Chunk{
		"chunk-1": {ID: "chunk-1", RefDocID: "doc-a", Text: "passage"},
	}}
	g := got.ThoughtGraph{
		ChunkSources: []got.Source{
			{DocID: "doc-a", ChunkID: "chunk-1"},
			{DocID: "doc-a", ChunkID: "chunk-1"},
		},
	}

	chunks := gotContextChunks(g, searcher)
	if len(chunks) != 1 {
		t.Fatalf("got %d context chunks, want 1 (same chunk id must still dedup): %+v", len(chunks), chunks)
	}
}
