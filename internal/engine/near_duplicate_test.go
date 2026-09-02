package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

const nearDuplicateBaseText = "The quick brown fox jumps over the lazy dog while the cat watches from the window and the sun sets slowly over the hills. The quick brown fox jumps over the lazy dog while the cat watches from the window and the sun sets slowly over the hills."

func TestNearDuplicateDocumentIsSkippedAndFlagged(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: nearDuplicateBaseText})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: strings.Replace(nearDuplicateBaseText, "quick", "fast", 1)})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	for _, c := range chunks {
		if c.RefDocID == "notes/b" {
			t.Fatalf("near-duplicate document b was indexed, got chunk %+v", c)
		}
	}

	fingerprints, err := vs.ListDocumentFingerprints(ctx)
	if err != nil {
		t.Fatalf("ListDocumentFingerprints: %v", err)
	}
	if len(fingerprints) != 2 {
		t.Fatalf("fingerprint rows = %+v, want 2", fingerprints)
	}
	byID := make(map[string]vector.DocumentFingerprint, len(fingerprints))
	for _, fp := range fingerprints {
		byID[fp.RefDocID] = fp
	}
	if byID["notes/a"].DuplicateOf != "" {
		t.Fatalf("original document flagged as duplicate: %+v", byID["notes/a"])
	}
	if byID["notes/b"].DuplicateOf != "notes/a" {
		t.Fatalf("duplicate flag = %q, want notes/a", byID["notes/b"].DuplicateOf)
	}
}

func TestSkippedNearDuplicateRemainsSkippedOnReindex(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: nearDuplicateBaseText})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: strings.Replace(nearDuplicateBaseText, "quick", "fast", 1)})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("reindex b: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	for _, c := range chunks {
		if c.RefDocID == "notes/b" {
			t.Fatalf("reindexed duplicate document b should stay skipped, got chunk %+v", c)
		}
	}
}

func TestNearDuplicateReconsideredAfterTargetDeleted(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: nearDuplicateBaseText})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: strings.Replace(nearDuplicateBaseText, "quick", "fast", 1)})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}
	if err := ix.RemoveDocumentBySourceID(ctx, "notes", "a"); err != nil {
		t.Fatalf("remove a: %v", err)
	}
	// b was never re-touched, so a stale duplicate_of=notes/a row on b is
	// exactly the scenario a full reindex sweep or a later edit of b would
	// re-evaluate; force that re-evaluation here.
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("reindex b after a removed: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	found := false
	for _, c := range chunks {
		if c.RefDocID == "notes/b" {
			found = true
		}
	}
	if !found {
		t.Fatal("b should be reindexed once its duplicate target a was deleted, got no chunks for notes/b")
	}
}

func TestNearDuplicateReconsideredAfterContentDiverges(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: nearDuplicateBaseText})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: strings.Replace(nearDuplicateBaseText, "quick", "fast", 1)})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}

	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "A completely unrelated report about quarterly sales figures in the northern region, covering revenue, headcount, and warehouse capacity changes."})
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("reindex b after content diverged: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	found := false
	for _, c := range chunks {
		if c.RefDocID == "notes/b" {
			found = true
		}
	}
	if !found {
		t.Fatal("b should be reindexed once its content no longer resembles a, got no chunks for notes/b")
	}

	fingerprints, err := vs.ListDocumentFingerprints(ctx)
	if err != nil {
		t.Fatalf("ListDocumentFingerprints: %v", err)
	}
	for _, fp := range fingerprints {
		if fp.RefDocID == "notes/b" && fp.DuplicateOf != "" {
			t.Fatalf("b still flagged as duplicate of %q after content diverged", fp.DuplicateOf)
		}
	}
}

type failingFingerprintStore struct {
	*sqlite.VectorStore
	fail bool
}

func (f *failingFingerprintStore) ListDocumentFingerprints(ctx context.Context) ([]vector.DocumentFingerprint, error) {
	if f.fail {
		return nil, errors.New("fingerprint list boom")
	}
	return f.VectorStore.ListDocumentFingerprints(ctx)
}

func TestNearDuplicateDetectionFailsOpenWhenFingerprintStoreErrors(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: nearDuplicateBaseText})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: strings.Replace(nearDuplicateBaseText, "quick", "fast", 1)})

	db := openTestDB(t)
	vs := &failingFingerprintStore{VectorStore: sqlite.NewVectorStore(db), fail: true}
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range chunks {
		seen[c.RefDocID] = true
	}
	if !seen["notes/a"] || !seen["notes/b"] {
		t.Fatalf("both documents should index when fingerprint lookup fails, got %v", seen)
	}
}
