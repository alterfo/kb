package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
)

func ftsTestDB(t *testing.T) (*DB, *VectorStore) {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, NewVectorStore(db)
}

func candidateIDs(results []bm25.ScoredID) []string {
	ids := make([]string, 0, len(results))
	for _, res := range results {
		ids = append(ids, res.ID)
	}
	return ids
}

func TestFTS5IndexMatchesLegacyBM25Candidates(t *testing.T) {
	db, vs := ftsTestDB(t)
	ctx := context.Background()

	chunks := []vector.Chunk{
		{ID: "fox", RefDocID: "fox", Text: "the quick brown fox jumps over the lazy dog", FilePath: "fox.md", FileName: "fox.md", Source: "test"},
		{ID: "graph", RefDocID: "graph", Text: "graph databases power knowledge retrieval systems", FilePath: "graph.md", FileName: "graph.md", Source: "test"},
		{ID: "fts", RefDocID: "fts", Text: "sqlite full text search with fts5 virtual tables", FilePath: "fts.md", FileName: "fts.md", Source: "test"},
	}
	if err := vs.Upsert(ctx, chunks); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	legacy := bm25.New()
	if err := legacy.Refresh(ctx, db, vs); err != nil {
		t.Fatalf("legacy Refresh: %v", err)
	}
	fts := NewFTS5Index(db)
	if err := fts.Refresh(ctx, db, vs); err != nil {
		t.Fatalf("fts5 Refresh: %v", err)
	}

	queries := []string{
		"fox",
		"retrieval",
		"fts5",
		"virtual tables",
		"fox retrieval",
	}
	for _, query := range queries {
		want := candidateIDs(legacy.Search(query, 10))
		got := candidateIDs(fts.Search(query, 10))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("query %q candidates = %v, want legacy %v", query, got, want)
		}
	}
}

func TestFTS5RefreshTracksCorpusVersionAndExcludesClosedChunks(t *testing.T) {
	db, vs := ftsTestDB(t)
	ctx := context.Background()

	chunk := vector.Chunk{ID: "old", RefDocID: "doc", Text: "old active text", FilePath: "doc.md", FileName: "doc.md", Source: "test"}
	if err := vs.Upsert(ctx, []vector.Chunk{chunk}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	fts := NewFTS5Index(db)
	if err := fts.Refresh(ctx, db, vs); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if got := candidateIDs(fts.Search("active", 10)); !reflect.DeepEqual(got, []string{"old"}) {
		t.Fatalf("before close candidates = %v, want [old]", got)
	}

	if err := vs.SoftCloseByDoc(ctx, "doc"); err != nil {
		t.Fatalf("SoftCloseByDoc: %v", err)
	}
	if err := fts.Refresh(ctx, db, vs); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if got := candidateIDs(fts.Search("active", 10)); len(got) != 0 {
		t.Fatalf("after close candidates = %v, want empty", got)
	}
}

func TestFTS5QuerySanitizesUserInput(t *testing.T) {
	got := fts5Query(`" OR " AND * sqlite:fts5`)
	want := `"or" OR "and" OR "sqlite" OR "fts5"`
	if got != want {
		t.Fatalf("fts5Query = %q, want %q", got, want)
	}
}

func TestFTS5ChunkReturnsActiveChunk(t *testing.T) {
	db, vs := ftsTestDB(t)
	ctx := context.Background()

	chunk := vector.Chunk{ID: "c1", RefDocID: "doc", Text: "searchable content", FilePath: "doc.md", FileName: "doc.md", Source: "test"}
	if err := vs.Upsert(ctx, []vector.Chunk{chunk}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	fts := NewFTS5Index(db)
	if err := fts.Refresh(ctx, db, vs); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, ok := fts.Chunk("c1")
	if !ok || got.Text != chunk.Text {
		t.Fatalf("Chunk(c1) = %+v, %v; want %q, true", got, ok, chunk.Text)
	}
	if _, ok := fts.Chunk("missing"); ok {
		t.Fatal("Chunk(missing) = _, true; want false")
	}
}
