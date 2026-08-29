package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/store/vector"
)

func TestEnsureDimCreatesAndPersists(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	if err := vs.EnsureDim(ctx, 384); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}
	n, ok, err := db.getMetaInt(ctx, metaKeyEmbedDim)
	if err != nil || !ok || n != 384 {
		t.Fatalf("meta embed_dim = (%d,%v,%v), want (384,true,nil)", n, ok, err)
	}

	if err := vs.EnsureDim(ctx, 384); err != nil {
		t.Fatalf("second EnsureDim with same dim should succeed: %v", err)
	}
}

func TestEnsureDimMismatchFailsLoud(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	if err := vs.EnsureDim(ctx, 384); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}
	err := vs.EnsureDim(ctx, 768)
	if err == nil {
		t.Fatal("EnsureDim with mismatched dim should fail")
	}
	if !errors.Is(err, vector.ErrDimMismatch) {
		t.Fatalf("error = %v, want wrapping ErrDimMismatch", err)
	}
}

func TestEnsureDimRejectsNonPositive(t *testing.T) {
	db := openTestDB(t)
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(context.Background(), 0); err == nil {
		t.Fatal("EnsureDim(0) should fail")
	}
	if err := vs.EnsureDim(context.Background(), -1); err == nil {
		t.Fatal("EnsureDim(-1) should fail")
	}
}

func TestReembedClearsDimAndEmbeddings(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	if err := vs.EnsureDim(ctx, 3); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}
	if err := vs.Upsert(ctx, []vector.Chunk{{ID: "a", RefDocID: "doc1", Text: "hi", Source: "notes", Embedding: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := vs.SetDocHash(ctx, "doc1", "hash-1"); err != nil {
		t.Fatalf("SetDocHash: %v", err)
	}

	if err := vs.Reembed(ctx); err != nil {
		t.Fatalf("Reembed: %v", err)
	}

	if _, ok, err := db.getMetaInt(ctx, metaKeyEmbedDim); err != nil || ok {
		t.Fatalf("embed_dim should be cleared, got ok=%v err=%v", ok, err)
	}
	if err := vs.EnsureDim(ctx, 5); err != nil {
		t.Fatalf("EnsureDim with new dim after Reembed should succeed: %v", err)
	}

	all, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected chunk row to survive Reembed, got %d rows", len(all))
	}
	if _, ok, err := vs.DocHash(ctx, "doc1"); err != nil || ok {
		t.Fatalf("doc hash should be cleared by Reembed, got ok=%v err=%v", ok, err)
	}
}

func mustUpsert(t *testing.T, vs *VectorStore, chunks []vector.Chunk) {
	t.Helper()
	if err := vs.Upsert(context.Background(), chunks); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}

func TestUpsertAndQueryTopK(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "close", RefDocID: "doc1", Text: "close", Source: "notes", Embedding: []float32{1, 0}},
		{ID: "mid", RefDocID: "doc1", Text: "mid", Source: "notes", Embedding: []float32{0.7, 0.7}},
		{ID: "far", RefDocID: "doc1", Text: "far", Source: "notes", Embedding: []float32{0, 1}},
	})

	results, err := vs.Query(ctx, []float32{1, 0}, 2, vector.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].ID != "close" {
		t.Fatalf("results[0].ID = %q, want %q", results[0].ID, "close")
	}
	if results[1].ID != "mid" {
		t.Fatalf("results[1].ID = %q, want %q", results[1].ID, "mid")
	}
	if results[0].Score < results[1].Score {
		t.Fatalf("results not sorted descending by score: %v", results)
	}
}

func TestUpsertRejectsMismatchedEmbeddingLen(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 3); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	err := vs.Upsert(ctx, []vector.Chunk{{ID: "a", RefDocID: "doc1", Text: "hi", Source: "notes", Embedding: []float32{1, 0}}})
	if err == nil {
		t.Fatal("Upsert with wrong embedding length should fail")
	}
	if !errors.Is(err, vector.ErrDimMismatch) {
		t.Fatalf("error = %v, want wrapping ErrDimMismatch", err)
	}
}

func TestUpsertUpdatesExistingChunkWithoutDuplicate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	mustUpsert(t, vs, []vector.Chunk{{ID: "a", RefDocID: "doc1", Text: "v1", Source: "notes", Embedding: []float32{1, 0}}})
	mustUpsert(t, vs, []vector.Chunk{{ID: "a", RefDocID: "doc1", Text: "v2", Source: "notes", Embedding: []float32{0, 1}}})

	all, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d chunks, want 1 (no duplicates)", len(all))
	}
	if all[0].Text != "v2" {
		t.Fatalf("Text = %q, want %q", all[0].Text, "v2")
	}
}

func TestDeleteByDocRemovesAllItsChunksOnly(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "doc1-0", RefDocID: "doc1", Text: "a", Source: "notes", Embedding: []float32{1, 0}},
		{ID: "doc1-1", RefDocID: "doc1", Text: "b", Source: "notes", Embedding: []float32{0, 1}},
		{ID: "doc2-0", RefDocID: "doc2", Text: "c", Source: "notes", Embedding: []float32{1, 1}},
	})

	if err := vs.DeleteByDoc(ctx, "doc1"); err != nil {
		t.Fatalf("DeleteByDoc: %v", err)
	}

	all, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(all) != 1 || all[0].RefDocID != "doc2" {
		t.Fatalf("got %+v, want only doc2's chunk to remain", all)
	}
}

func TestDeleteByDocThenReinsertHasNoDuplicates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "doc1-0", RefDocID: "doc1", Text: "a", Source: "notes", Embedding: []float32{1, 0}},
		{ID: "doc1-1", RefDocID: "doc1", Text: "b", Source: "notes", Embedding: []float32{0, 1}},
	})
	if err := vs.DeleteByDoc(ctx, "doc1"); err != nil {
		t.Fatalf("DeleteByDoc: %v", err)
	}
	mustUpsert(t, vs, []vector.Chunk{
		{ID: "doc1-0", RefDocID: "doc1", Text: "a2", Source: "notes", Embedding: []float32{1, 0}},
	})

	all, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d chunks, want 1", len(all))
	}
}

func TestQueryFilterBySource(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "gh", RefDocID: "doc1", Text: "gh", Source: "github", Embedding: []float32{1, 0}},
		{ID: "gl", RefDocID: "doc2", Text: "gl", Source: "gitlab", Embedding: []float32{1, 0}},
		{ID: "note", RefDocID: "doc3", Text: "note", Source: "notes", Embedding: []float32{1, 0}},
	})

	results, err := vs.Query(ctx, []float32{1, 0}, 10, vector.Filter{Sources: []string{"github"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 || results[0].ID != "gh" {
		t.Fatalf("got %+v, want only github chunk", results)
	}
}

func TestQueryFilterByVirtualCollectionMultipleSources(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "gh", RefDocID: "doc1", Text: "gh", Source: "github", Embedding: []float32{1, 0}},
		{ID: "gl", RefDocID: "doc2", Text: "gl", Source: "gitlab", Embedding: []float32{1, 0}},
		{ID: "note", RefDocID: "doc3", Text: "note", Source: "notes", Embedding: []float32{1, 0}},
	})

	// "code" virtual collection == {github, gitlab}
	results, err := vs.Query(ctx, []float32{1, 0}, 10, vector.Filter{Sources: []string{"github", "gitlab"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (github+gitlab)", len(results))
	}
	for _, r := range results {
		if r.Source != "github" && r.Source != "gitlab" {
			t.Fatalf("unexpected source %q leaked through virtual collection filter", r.Source)
		}
	}
}

func TestAllForBM25ReturnsEveryChunkRegardlessOfEmbedding(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "a", RefDocID: "doc1", Text: "has embedding", Source: "notes", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc2", Text: "no embedding", Source: "notes"},
	})

	all, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d chunks, want 2", len(all))
	}
}

func TestUpsertBumpsCorpusVersion(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	before, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	mustUpsert(t, vs, []vector.Chunk{{ID: "a", RefDocID: "doc1", Text: "x", Source: "notes", Embedding: []float32{1, 0}}})
	after, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if after <= before {
		t.Fatalf("corpus_version did not bump on Upsert: before=%d after=%d", before, after)
	}

	if err := vs.DeleteByDoc(ctx, "doc1"); err != nil {
		t.Fatalf("DeleteByDoc: %v", err)
	}
	afterDelete, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if afterDelete <= after {
		t.Fatalf("corpus_version did not bump on DeleteByDoc: after=%d afterDelete=%d", after, afterDelete)
	}
}

func TestEncodeDecodeVectorRoundTrip(t *testing.T) {
	orig := []float32{1.5, -2.25, 0, 3.3333}
	got := decodeVector(encodeVector(orig))
	if len(got) != len(orig) {
		t.Fatalf("len(got)=%d, want %d", len(got), len(orig))
	}
	for i := range orig {
		if got[i] != orig[i] {
			t.Fatalf("got[%d]=%v, want %v", i, got[i], orig[i])
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	if got := cosineSimilarity([]float32{1, 0}, []float32{1, 0}); got < 0.999 {
		t.Fatalf("identical vectors cosine = %v, want ~1", got)
	}
	if got := cosineSimilarity([]float32{1, 0}, []float32{0, 1}); got > 0.001 || got < -0.001 {
		t.Fatalf("orthogonal vectors cosine = %v, want ~0", got)
	}
	if got := cosineSimilarity([]float32{1, 0}, []float32{-1, 0}); got > -0.999 {
		t.Fatalf("opposite vectors cosine = %v, want ~-1", got)
	}
	if got := cosineSimilarity(nil, []float32{1, 0}); got != 0 {
		t.Fatalf("empty vector cosine = %v, want 0", got)
	}
	if got := cosineSimilarity([]float32{0, 0}, []float32{1, 0}); got != 0 {
		t.Fatalf("zero-norm vector cosine = %v, want 0", got)
	}
}

func TestAllForBM25CorruptMetadataFails(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	if err := vs.Upsert(ctx, []vector.Chunk{{
		ID: "doc#0", RefDocID: "doc", Text: "hello", FilePath: "doc.md",
		FileName: "doc.md", Source: "notes", TokenCount: 1, ChunkIndex: 0,
		Metadata: map[string]string{"project": "alpha"},
	}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if _, err := db.sql.ExecContext(ctx, `UPDATE chunks SET metadata = ? WHERE id = ?`, `{"broken": `, "doc#0"); err != nil {
		t.Fatalf("corrupt metadata: %v", err)
	}

	if _, err := vs.AllForBM25(ctx); err == nil {
		t.Fatal("expected error for corrupt metadata JSON")
	}
}

func TestDecodeMetadataInvalidJSON(t *testing.T) {
	if _, err := decodeMetadata([]byte("{")); err == nil {
		t.Fatal("expected error decoding invalid JSON")
	}
	if m, err := decodeMetadata(nil); err != nil || m != nil {
		t.Fatalf("decodeMetadata(nil) = %v, %v; want nil, nil", m, err)
	}
}

func TestChunkLifecycleFieldsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	mustUpsert(t, vs, []vector.Chunk{{
		ID: "a", RefDocID: "doc1", Text: "x", FilePath: "p/a", FileName: "a",
		Source: "notes", TokenCount: 1, ChunkIndex: 0,
		CreatedAt:    "2026-08-21T10:00:00Z",
		ValidTo:      "2026-08-21T11:00:00Z",
		Replaces:     "old#0",
		SupersededBy: "newer-doc",
	}})

	got, err := vs.ChunksByDoc(ctx, "doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d chunks, want 1", len(got))
	}
	c := got[0]
	if c.CreatedAt != "2026-08-21T10:00:00Z" {
		t.Fatalf("CreatedAt = %q, want %q", c.CreatedAt, "2026-08-21T10:00:00Z")
	}
	if c.ValidTo != "2026-08-21T11:00:00Z" {
		t.Fatalf("ValidTo = %q, want %q", c.ValidTo, "2026-08-21T11:00:00Z")
	}
	if c.Replaces != "old#0" {
		t.Fatalf("Replaces = %q, want %q", c.Replaces, "old#0")
	}
	if c.SupersededBy != "newer-doc" {
		t.Fatalf("SupersededBy = %q, want %q", c.SupersededBy, "newer-doc")
	}
}

func TestUpsertSetsCreatedAtWhenEmptyAndPreservesOnConflict(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	mustUpsert(t, vs, []vector.Chunk{{
		ID: "a", RefDocID: "doc1", Text: "v1", Source: "notes",
		CreatedAt: "2026-08-21T10:00:00Z",
	}})

	got, err := vs.ChunksByDoc(ctx, "doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	if len(got) != 1 || got[0].CreatedAt != "2026-08-21T10:00:00Z" {
		t.Fatalf("created_at not preserved across upsert: %+v", got)
	}

	mustUpsert(t, vs, []vector.Chunk{{
		ID: "a", RefDocID: "doc1", Text: "v2", Source: "notes",
	}})

	got, err = vs.ChunksByDoc(ctx, "doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc after conflict: %v", err)
	}
	if len(got) != 1 || got[0].CreatedAt != "2026-08-21T10:00:00Z" {
		t.Fatalf("conflict update must preserve created_at: %+v", got)
	}
	if got[0].Text != "v2" {
		t.Fatalf("Text = %q, want %q", got[0].Text, "v2")
	}

	mustUpsert(t, vs, []vector.Chunk{{
		ID: "b", RefDocID: "doc1", Text: "fresh", Source: "notes",
	}})
	got, err = vs.ChunksByDoc(ctx, "doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc after insert: %v", err)
	}
	var freshCreated string
	for _, c := range got {
		if c.ID == "b" {
			freshCreated = c.CreatedAt
		}
	}
	if freshCreated == "" {
		t.Fatal("insert without CreatedAt should stamp created_at")
	}
}

func TestQueryAndAllForBM25ExcludeClosedChunks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	if err := vs.EnsureDim(ctx, 2); err != nil {
		t.Fatalf("EnsureDim: %v", err)
	}

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "a", RefDocID: "doc1", Text: "stale", Source: "notes", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc2", Text: "fresh", Source: "notes", Embedding: []float32{1, 0}},
	})
	if err := vs.SoftCloseByDoc(ctx, "doc1"); err != nil {
		t.Fatalf("SoftCloseByDoc: %v", err)
	}

	results, err := vs.Query(ctx, []float32{1, 0}, 10, vector.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 || results[0].ID != "b" {
		t.Fatalf("Query returned %+v, want only active chunk b", results)
	}

	all, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(all) != 1 || all[0].ID != "b" {
		t.Fatalf("AllForBM25 returned %+v, want only active chunk b", all)
	}

	byDoc, err := vs.ChunksByDoc(ctx, "doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	if len(byDoc) != 1 || byDoc[0].ID != "a" || byDoc[0].ValidTo == "" {
		t.Fatalf("ChunksByDoc must return closed versions with valid_to: %+v", byDoc)
	}
}

func TestReplaceByDocRollsBackSoftCloseOnUpsertFailure(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "old", RefDocID: "doc1", Text: "original", Source: "notes"},
	})

	err := vs.ReplaceByDoc(ctx, "doc1", []vector.Chunk{
		{RefDocID: "doc1", Text: "replacement", Source: "notes"},
	})
	if err == nil {
		t.Fatal("ReplaceByDoc with a missing chunk id must fail")
	}

	chunks, err := vs.ChunksByDoc(ctx, "doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	var active int
	for _, c := range chunks {
		if c.ValidTo == "" {
			active++
		}
	}
	if active != 1 || chunks[0].ID != "old" {
		t.Fatalf("failed ReplaceByDoc must leave the old chunk active, got %+v", chunks)
	}
}

func TestSoftCloseByDocClosesOnlyActiveChunksOfDoc(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "a", RefDocID: "doc1", Text: "a1", Source: "notes"},
		{ID: "b", RefDocID: "doc1", Text: "b1", Source: "notes"},
		{ID: "c", RefDocID: "doc2", Text: "c1", Source: "notes"},
	})

	before, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if err := vs.SoftCloseByDoc(ctx, "doc1"); err != nil {
		t.Fatalf("SoftCloseByDoc: %v", err)
	}
	after, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if after <= before {
		t.Fatalf("SoftCloseByDoc must bump corpus_version: before=%d after=%d", before, after)
	}

	doc1, err := vs.ChunksByDoc(ctx, "doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc(doc1): %v", err)
	}
	if len(doc1) != 2 {
		t.Fatalf("SoftCloseByDoc must keep closed rows as history, got %d", len(doc1))
	}
	for _, c := range doc1 {
		if c.ValidTo == "" {
			t.Fatalf("chunk %q of doc1 should be closed, got %+v", c.ID, c)
		}
	}

	doc2, err := vs.ChunksByDoc(ctx, "doc2")
	if err != nil {
		t.Fatalf("ChunksByDoc(doc2): %v", err)
	}
	if len(doc2) != 1 || doc2[0].ValidTo != "" {
		t.Fatalf("doc2 chunk must stay active: %+v", doc2)
	}

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "a", RefDocID: "doc1", Text: "a2", Source: "notes"},
		{ID: "d", RefDocID: "doc1", Text: "d1", Source: "notes", Replaces: "a,b"},
	})
	doc1, err = vs.ChunksByDoc(ctx, "doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc(doc1) after re-upsert: %v", err)
	}
	byID := map[string]vector.Chunk{}
	for _, c := range doc1 {
		byID[c.ID] = c
	}
	if byID["a"].ValidTo == "" {
		t.Fatal("re-upserting a closed chunk must not reopen it")
	}
	if byID["d"].ValidTo != "" {
		t.Fatal("new chunk d must be active")
	}
	if byID["d"].Replaces != "a,b" {
		t.Fatalf("Replaces = %q, want %q", byID["d"].Replaces, "a,b")
	}
}

func TestSetSupersededAndClearSupersededBy(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "x", RefDocID: "doc1", Text: "x1", Source: "notes"},
		{ID: "y", RefDocID: "doc2", Text: "y1", Source: "notes"},
	})

	before, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if err := vs.SetSuperseded(ctx, []string{"x"}, "doc2"); err != nil {
		t.Fatalf("SetSuperseded: %v", err)
	}
	after, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if after <= before {
		t.Fatalf("SetSuperseded must bump corpus_version")
	}

	all, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	byID := map[string]vector.Chunk{}
	for _, c := range all {
		byID[c.ID] = c
	}
	if byID["x"].SupersededBy != "doc2" {
		t.Fatalf("chunk x SupersededBy = %q, want %q", byID["x"].SupersededBy, "doc2")
	}
	if byID["y"].SupersededBy != "" {
		t.Fatalf("chunk y must not be superseded: %+v", byID["y"])
	}

	if err := vs.ClearSupersededBy(ctx, "doc2"); err != nil {
		t.Fatalf("ClearSupersededBy: %v", err)
	}
	afterClear, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if afterClear <= after {
		t.Fatalf("ClearSupersededBy must bump corpus_version")
	}

	all, err = vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	for _, c := range all {
		if c.SupersededBy != "" {
			t.Fatalf("chunk %q still superseded after clear: %+v", c.ID, c)
		}
	}

	beforeNoop, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if err := vs.SetSuperseded(ctx, nil, "doc3"); err != nil {
		t.Fatalf("SetSuperseded(nil): %v", err)
	}
	afterNoop, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if afterNoop != beforeNoop {
		t.Fatal("SetSuperseded with no chunk ids must not bump corpus_version")
	}
}

func TestClearSupersededOnDocClearsOnlyActiveChunksOfDoc(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	mustUpsert(t, vs, []vector.Chunk{
		{ID: "x1", RefDocID: "doc1", Text: "x1", Source: "notes"},
		{ID: "y1", RefDocID: "doc2", Text: "y1", Source: "notes"},
	})
	if err := vs.SetSuperseded(ctx, []string{"x1", "y1"}, "doc3"); err != nil {
		t.Fatalf("SetSuperseded: %v", err)
	}
	if err := vs.SoftCloseByDoc(ctx, "doc1"); err != nil {
		t.Fatalf("SoftCloseByDoc: %v", err)
	}
	mustUpsert(t, vs, []vector.Chunk{
		{ID: "x2", RefDocID: "doc1", Text: "x2", Source: "notes", Replaces: "x1"},
	})
	if err := vs.SetSuperseded(ctx, []string{"x2"}, "doc3"); err != nil {
		t.Fatalf("SetSuperseded x2: %v", err)
	}

	before, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if err := vs.ClearSupersededOnDoc(ctx, "doc1"); err != nil {
		t.Fatalf("ClearSupersededOnDoc: %v", err)
	}
	after, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	if after <= before {
		t.Fatal("ClearSupersededOnDoc must bump corpus_version")
	}

	all, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	byID := map[string]vector.Chunk{}
	for _, c := range all {
		byID[c.ID] = c
	}
	if byID["x2"].SupersededBy != "" {
		t.Fatalf("active doc1 chunk x2 still superseded: %+v", byID["x2"])
	}
	if byID["y1"].SupersededBy != "doc3" {
		t.Fatalf("other doc's chunk must keep its mark: %+v", byID["y1"])
	}
}

func TestUpsertRejectsMissingID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)

	err := vs.Upsert(ctx, []vector.Chunk{{RefDocID: "doc1", Text: "no id", Source: "notes"}})
	if err == nil {
		t.Fatal("Upsert with empty chunk id should fail")
	}
}

func TestDocHashOnClosedDBFails(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	db.Close()

	if _, _, err := vs.DocHash(ctx, "doc1"); err == nil {
		t.Fatal("DocHash on closed DB should fail")
	}
}

func TestLifecycleOpsOnClosedDBFail(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	vs := NewVectorStore(db)
	db.Close()

	if err := vs.SoftCloseByDoc(ctx, "doc1"); err == nil {
		t.Fatal("SoftCloseByDoc on closed DB should fail")
	}
	if err := vs.SetSuperseded(ctx, []string{"a"}, "doc2"); err == nil {
		t.Fatal("SetSuperseded on closed DB should fail")
	}
	if err := vs.ClearSupersededBy(ctx, "doc2"); err == nil {
		t.Fatal("ClearSupersededBy on closed DB should fail")
	}
	if err := vs.SetDocHash(ctx, "doc1", "h"); err == nil {
		t.Fatal("SetDocHash on closed DB should fail")
	}
}
