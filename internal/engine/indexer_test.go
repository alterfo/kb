package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
)

func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func writeDoc(t *testing.T, root, relPath string, d connector.Document) {
	t.Helper()
	data, err := render.Render(d)
	if err != nil {
		t.Fatalf("render.Render: %v", err)
	}
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

type fakeEmbedder struct {
	dim   int
	calls int
	err   error
}

func (f *fakeEmbedder) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, f.dim)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}

type fakeChatClient struct {
	calls int
}

func (f *fakeChatClient) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.calls++
	return llm.ChatResponse{Content: `{"entities":[],"relations":[]}`}, nil
}

type scriptedChat struct {
	responses []string
	i         int
}

const (
	aliceBobJSON      = `{"entities":[{"name":"Alice","type":"person"},{"name":"Bob","type":"person"}],"relations":[{"source":"Alice","target":"Bob","type":"knows"}]}`
	aliceCarolJSON    = `{"entities":[{"name":"Alice","type":"person"},{"name":"Carol","type":"person"}],"relations":[{"source":"Alice","target":"Carol","type":"knows"}]}`
	aliceDanJSON      = `{"entities":[{"name":"Alice","type":"person"},{"name":"Dan","type":"person"}],"relations":[{"source":"Alice","target":"Dan","type":"knows"}]}`
	xavierYolandaJSON = `{"entities":[{"name":"Xavier","type":"person"},{"name":"Yolanda","type":"person"}],"relations":[{"source":"Xavier","target":"Yolanda","type":"knows"}]}`
)

func (s *scriptedChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if len(s.responses) == 0 {
		return llm.ChatResponse{Content: `{"entities":[],"relations":[]}`}, nil
	}
	idx := s.i
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}
	s.i++
	return llm.ChatResponse{Content: s.responses[idx]}, nil
}

func TestAddOrUpdateDocumentIndexesChunks(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Hello world."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	c := chunks[0]
	if c.RefDocID != "notes/doc1" || c.Source != "notes" || c.FilePath != "notes/doc1.md" {
		t.Fatalf("unexpected chunk: %+v", c)
	}
}

func TestAddOrUpdateDocumentIsIdempotentNoDuplicates(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Hello world."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
			t.Fatalf("AddOrUpdateDocument[%d]: %v", i, err)
		}
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1 (no duplicates)", len(chunks))
	}
}

func TestAddOrUpdateDocumentSkipsUnchangedContent(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Hello world."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	embed := &fakeEmbedder{dim: 4}
	gs := sqlite.NewGraphStore(db)
	chat := &fakeChatClient{}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), nil)
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, Embed: embed, EmbedModel: "test-embed", ChunkSize: 512})
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
			t.Fatalf("AddOrUpdateDocument[%d]: %v", i, err)
		}
	}
	if embed.calls != 1 {
		t.Fatalf("embed.calls = %d, want 1 (unchanged content should skip re-embedding)", embed.calls)
	}
	if chat.calls != 1 {
		t.Fatalf("chat.calls = %d, want 1 (unchanged content should skip graph re-extraction)", chat.calls)
	}
}

func TestAddOrUpdateDocumentReembedsAfterContentChanges(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Original text."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	embed := &fakeEmbedder{dim: 4}
	ix := NewIndexer(Config{Root: root, Vector: vs, Embed: embed, EmbedModel: "test-embed", ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Updated text."})
	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument (changed): %v", err)
	}
	if embed.calls != 2 {
		t.Fatalf("embed.calls = %d, want 2 (changed content must be re-embedded)", embed.calls)
	}
}

func TestReindexReportsSkippedForUnchangedDocuments(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "aaa"})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "bbb"})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if _, err := ix.Reindex(ctx, "notes"); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "changed"})

	res, err := ix.Reindex(ctx, "notes")
	if err != nil {
		t.Fatalf("Reindex (second pass): %v", err)
	}
	if res.Indexed != 1 || res.Skipped != 1 || res.Removed != 0 {
		t.Fatalf("Reindex result = %+v, want Indexed=1 (notes/b.md changed) Skipped=1 (notes/a.md unchanged)", res)
	}
}

func TestAddOrUpdateDocumentReplacesStaleChunksOnUpdate(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Original text."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Updated text."})
	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument (update): %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	if chunks[0].Text != "Updated text." {
		t.Fatalf("chunk text = %q, want %q", chunks[0].Text, "Updated text.")
	}
}

func TestAddOrUpdateDocumentSoftClosesPreviousChunksWithLineage(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Original text."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	embed := &fakeEmbedder{dim: 4}
	ix := NewIndexer(Config{Root: root, Vector: vs, Embed: embed, EmbedModel: "test-embed", ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Updated text."})
	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument (update): %v", err)
	}

	all, err := vs.ChunksByDoc(ctx, "notes/doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("soft-close must keep old versions as history, got %d chunks", len(all))
	}
	var oldChunk, newChunk *vector.Chunk
	for i := range all {
		c := &all[i]
		switch c.ValidTo {
		case "":
			newChunk = c
		default:
			oldChunk = c
		}
	}
	if oldChunk == nil || newChunk == nil {
		t.Fatalf("want one closed and one active chunk, got %+v", all)
	}
	if newChunk.Replaces != oldChunk.ID {
		t.Fatalf("lineage: new chunk %q replaces %q, want %q", newChunk.ID, newChunk.Replaces, oldChunk.ID)
	}
	if newChunk.ID == oldChunk.ID {
		t.Fatalf("versioned id expected on update, both chunks are %q", newChunk.ID)
	}

	scored, err := vs.Query(ctx, []float32{1, 0, 0, 0}, 10, vector.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(scored) != 1 || scored[0].ID != newChunk.ID {
		t.Fatalf("Query must return only the active chunk, got %+v", scored)
	}
	if scored[0].Text != "Updated text." {
		t.Fatalf("active chunk text = %q, want %q", scored[0].Text, "Updated text.")
	}
}

func TestAddOrUpdateDocumentEmbedsChunksAndEnsuresDim(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Hello world."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	embed := &fakeEmbedder{dim: 4}
	ix := NewIndexer(Config{Root: root, Vector: vs, Embed: embed, EmbedModel: "test-embed", ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}
	if embed.calls != 1 {
		t.Fatalf("embed.calls = %d, want 1", embed.calls)
	}

	scored, err := vs.Query(ctx, []float32{1, 0, 0, 0}, 5, vector.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(scored) != 1 {
		t.Fatalf("len(scored) = %d, want 1", len(scored))
	}
}

func TestAddOrUpdateDocumentFailsOpenWhenEmbedderErrors(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Hello world."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	embed := &fakeEmbedder{dim: 4, err: context.DeadlineExceeded}
	ix := NewIndexer(Config{Root: root, Vector: vs, Embed: embed, EmbedModel: "test-embed", ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument should fail open on embed error, got: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1 (indexed without vectors)", len(chunks))
	}
	if chunks[0].Embedding != nil {
		t.Fatalf("expected nil embedding on fail-open, got %v", chunks[0].Embedding)
	}
}

type failReplaceStore struct {
	vector.Store
	fail bool
}

func (s *failReplaceStore) ReplaceByDoc(ctx context.Context, docID string, chunks []vector.Chunk) error {
	if s.fail {
		return errors.New("replace boom")
	}
	return s.Store.ReplaceByDoc(ctx, docID, chunks)
}

func TestAddOrUpdateDocumentRollsBackWhenReplaceErrors(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Original text."})

	db := openTestDB(t)
	vs := &failReplaceStore{Store: sqlite.NewVectorStore(db)}
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("initial index: %v", err)
	}

	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Updated text."})
	vs.fail = true
	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err == nil {
		t.Fatal("update during replace failure must return an error")
	}

	chunks, err := vs.ChunksByDoc(ctx, "notes/doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	foundOld := false
	for _, c := range chunks {
		if c.Text == "Original text." && c.ValidTo == "" {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatalf("old chunk must remain active after failed replace, got %+v", chunks)
	}
}

func TestAddOrUpdateDocumentRetriesEmbeddingAfterFailure(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Hello world."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	embed := &fakeEmbedder{dim: 4, err: context.DeadlineExceeded}
	ix := NewIndexer(Config{Root: root, Vector: vs, Embed: embed, EmbedModel: "test-embed", ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("first index should fail open on embed error, got: %v", err)
	}
	if _, ok, err := vs.DocHash(ctx, "notes/doc1"); err != nil || ok {
		t.Fatalf("doc with missing embeddings must not be hashed, got ok=%v err=%v", ok, err)
	}

	embed.err = nil
	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("retry index: %v", err)
	}
	chunks, err := vs.ChunksByDoc(ctx, "notes/doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	var active []vector.Chunk
	for _, c := range chunks {
		if c.ValidTo == "" {
			active = append(active, c)
		}
	}
	if len(active) != 1 || len(active[0].Embedding) == 0 {
		t.Fatalf("retry must re-embed the document, got chunks=%+v", chunks)
	}
	if _, ok, err := vs.DocHash(ctx, "notes/doc1"); err != nil || !ok {
		t.Fatalf("doc hash should be recorded after successful embedding, got ok=%v err=%v", ok, err)
	}
}

func TestReindexPreservesEmbeddingsWhenEmbedderErrors(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{
		ID: "doc1", Source: "notes",
		Body: "First sentence about apples. Second sentence about bananas.",
	})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	embed := &fakeEmbedder{dim: 4}
	ix := NewIndexer(Config{Root: root, Vector: vs, Embed: embed, EmbedModel: "test-embed", ChunkSize: 8})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	before, err := vs.ChunksByDoc(ctx, "notes/doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("len(chunks) = %d, want 2", len(before))
	}
	for _, c := range before {
		if len(c.Embedding) == 0 {
			t.Fatalf("chunk %s indexed without embedding", c.ID)
		}
	}

	embed.err = errors.New("embedding outage")
	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("reindex during embed outage should fail open, got: %v", err)
	}
	after, err := vs.ChunksByDoc(ctx, "notes/doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	var active []vector.Chunk
	for _, c := range after {
		if c.ValidTo == "" {
			active = append(active, c)
		}
	}
	if len(active) != 2 {
		t.Fatalf("active chunks = %d, want 2 (closed versions kept as history)", len(active))
	}
	for _, c := range active {
		if len(c.Embedding) == 0 {
			t.Fatalf("chunk %s lost its embedding during reindex", c.ID)
		}
	}
}

func TestReindexEmbeddingFailureKeepsOnlyMatchingChunkVectors(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{
		ID: "doc1", Source: "notes",
		Body: "First sentence about apples. Second sentence about bananas.",
	})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	embed := &fakeEmbedder{dim: 4}
	ix := NewIndexer(Config{Root: root, Vector: vs, Embed: embed, EmbedModel: "test-embed", ChunkSize: 8})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("initial index: %v", err)
	}

	writeDoc(t, root, "notes/doc1.md", connector.Document{
		ID: "doc1", Source: "notes",
		Body: "First sentence about apples. Second sentence about cherries.",
	})
	embed.err = errors.New("embedding outage")
	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("reindex during embed outage should fail open, got: %v", err)
	}

	chunks, err := vs.ChunksByDoc(ctx, "notes/doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	var active []vector.Chunk
	for _, c := range chunks {
		if c.ValidTo == "" {
			active = append(active, c)
		}
	}
	if len(active) != 2 {
		t.Fatalf("active chunks = %d, want 2 (closed versions kept as history)", len(active))
	}
	byIndex := map[int]vector.Chunk{}
	for _, c := range active {
		byIndex[c.ChunkIndex] = c
	}
	if len(byIndex[0].Embedding) == 0 {
		t.Fatalf("unchanged chunk at index 0 lost its embedding: %+v", byIndex[0])
	}
	if len(byIndex[1].Embedding) > 0 {
		t.Fatalf("changed chunk at index 1 kept a stale embedding: %+v", byIndex[1])
	}
}

func TestAddOrUpdateDocumentInfersSourceFromPathWhenMissing(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "wiki", "page1.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte("Just a body, no frontmatter."), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "wiki/page1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 || chunks[0].Source != "wiki" {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
}

func TestInferSource(t *testing.T) {
	cases := map[string]string{
		"notes/doc1.md":            "notes",
		"github-myorg/issues/1.md": "github-myorg",
		"root.md":                  "",
		"/leading/slash.md":        "leading",
	}
	for path, want := range cases {
		if got := InferSource(path); got != want {
			t.Errorf("InferSource(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRemoveDocumentDeletesChunksAndPrunesGraph(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "hello world"})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(gs, nil, nil)
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	if err := gs.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "alice|person", Name: "Alice", Type: "person", SourceChunks: []string{"notes/doc1#0"}},
	}); err != nil {
		t.Fatalf("seed entity: %v", err)
	}

	if err := ix.RemoveDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected chunks removed, got %+v", chunks)
	}

	ents, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(ents) != 0 {
		t.Fatalf("expected entity pruned after RemoveDocument, got %+v", ents)
	}
}

func TestRemoveDocumentHardDeletesAndClearsSupersededBy(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Alpha content."})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Beta content."})

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

	aChunks, err := vs.ChunksByDoc(ctx, "notes/a")
	if err != nil {
		t.Fatalf("ChunksByDoc a: %v", err)
	}
	var aID string
	for _, c := range aChunks {
		if c.ValidTo == "" {
			aID = c.ID
		}
	}
	if aID == "" {
		t.Fatalf("no active chunk for notes/a: %+v", aChunks)
	}
	if err := vs.SetSuperseded(ctx, []string{aID}, "notes/b"); err != nil {
		t.Fatalf("SetSuperseded: %v", err)
	}

	if err := ix.RemoveDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}

	bChunks, err := vs.ChunksByDoc(ctx, "notes/b")
	if err != nil {
		t.Fatalf("ChunksByDoc b: %v", err)
	}
	if len(bChunks) != 0 {
		t.Fatalf("RemoveDocument must hard-delete b's chunks (history included), got %+v", bChunks)
	}

	aChunks, err = vs.ChunksByDoc(ctx, "notes/a")
	if err != nil {
		t.Fatalf("ChunksByDoc a after removal: %v", err)
	}
	for _, c := range aChunks {
		if c.SupersededBy != "" {
			t.Fatalf("chunk %s still superseded by removed doc %q", c.ID, c.SupersededBy)
		}
	}
}

func TestBlastRadiusSupersedesOverlappingChunks(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Alice knows Bob."})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Alice knows Carol."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	chat := &scriptedChat{responses: []string{
		aliceBobJSON, `{"title":"Alice Bob","summary":"AB"}`,
		aliceCarolJSON, `{"title":"Alice Carol","summary":"AC"}`,
	}}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), graph.NewSummarizer(chat, "test-model"))
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}

	chunks, err := vs.ChunksByDoc(ctx, "notes/a")
	if err != nil {
		t.Fatalf("ChunksByDoc a: %v", err)
	}
	var aActive []vector.Chunk
	for _, c := range chunks {
		if c.ValidTo == "" {
			aActive = append(aActive, c)
		}
	}
	if len(aActive) != 1 {
		t.Fatalf("active chunks of a = %+v, want exactly 1", aActive)
	}
	if aActive[0].SupersededBy != "notes/b" {
		t.Fatalf("chunk %s superseded_by = %q, want notes/b (shared entity Alice)", aActive[0].ID, aActive[0].SupersededBy)
	}

	bChunks, err := vs.ChunksByDoc(ctx, "notes/b")
	if err != nil {
		t.Fatalf("ChunksByDoc b: %v", err)
	}
	for _, c := range bChunks {
		if c.ValidTo == "" && c.SupersededBy != "" {
			t.Fatalf("doc b's own chunk %s must not be superseded, got %q", c.ID, c.SupersededBy)
		}
	}
}

func TestBlastRadiusRemovalClearsSupersededBy(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Alice knows Bob."})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Alice knows Carol."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	chat := &scriptedChat{responses: []string{
		aliceBobJSON, `{"title":"Alice Bob","summary":"AB"}`,
		aliceCarolJSON, `{"title":"Alice Carol","summary":"AC"}`,
	}}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), graph.NewSummarizer(chat, "test-model"))
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}

	if err := ix.RemoveDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("RemoveDocument b: %v", err)
	}

	chunks, err := vs.ChunksByDoc(ctx, "notes/a")
	if err != nil {
		t.Fatalf("ChunksByDoc a: %v", err)
	}
	for _, c := range chunks {
		if c.ValidTo == "" && c.SupersededBy != "" {
			t.Fatalf("chunk %s still superseded by removed doc, got %q", c.ID, c.SupersededBy)
		}
	}
}

type failingSupersedeStore struct {
	vector.Store
	fail bool
}

func (f *failingSupersedeStore) SetSuperseded(ctx context.Context, chunkIDs []string, byRefDocID string) error {
	if f.fail {
		return errors.New("supersede boom")
	}
	return f.Store.SetSuperseded(ctx, chunkIDs, byRefDocID)
}

func TestBlastRadiusFailOpenOnSupersedeError(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Alice knows Bob."})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Alice knows Carol."})

	db := openTestDB(t)
	vs := &failingSupersedeStore{Store: sqlite.NewVectorStore(db), fail: true}
	gs := sqlite.NewGraphStore(db)
	chat := &scriptedChat{responses: []string{
		aliceBobJSON, `{"title":"Alice Bob","summary":"AB"}`,
		aliceCarolJSON, `{"title":"Alice Carol","summary":"AC"}`,
	}}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), graph.NewSummarizer(chat, "test-model"))
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b must fail open on supersede error: %v", err)
	}

	chunks, err := vs.ChunksByDoc(ctx, "notes/b")
	if err != nil {
		t.Fatalf("ChunksByDoc b: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("doc b chunks = %+v, want 1 (indexing completed despite supersede failure)", chunks)
	}
}

func TestRemoveDocumentOfAlreadyDeletedFileIsNoop(t *testing.T) {
	root := t.TempDir()
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})

	if err := ix.RemoveDocument(context.Background(), "notes/never-existed.md"); err != nil {
		t.Fatalf("RemoveDocument on missing doc should be a no-op, got: %v", err)
	}
}

func TestReindexSingleFile(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "hello world"})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	res, err := ix.Reindex(ctx, "notes/doc1.md")
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if res.Indexed != 1 || res.Removed != 0 {
		t.Fatalf("Reindex result = %+v, want Indexed=1 Removed=0", res)
	}
}

func TestReindexMissingPathRemoves(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "hello world"})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if _, err := ix.Reindex(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "notes", "doc1.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	res, err := ix.Reindex(ctx, "notes/doc1.md")
	if err != nil {
		t.Fatalf("Reindex (missing): %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("Reindex result = %+v, want Removed=1", res)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected no chunks left, got %+v", chunks)
	}
}

func TestReindexDirectorySubtree(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "aaa"})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "bbb"})
	writeDoc(t, root, "other/c.md", connector.Document{ID: "c", Source: "other", Body: "ccc"})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	res, err := ix.Reindex(ctx, "notes")
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if res.Indexed != 2 {
		t.Fatalf("Indexed = %d, want 2", res.Indexed)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2 (other/c.md untouched)", len(chunks))
	}
}

func TestBuildAllIndexesEverythingUnderRoot(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "aaa"})
	writeDoc(t, root, "wiki/b.md", connector.Document{ID: "b", Source: "wiki", Body: "bbb"})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	res, err := ix.BuildAll(ctx)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if res.Indexed != 2 || res.Removed != 0 {
		t.Fatalf("BuildAll result = %+v, want Indexed=2 Removed=0", res)
	}
}

func TestBuildAllGCsDeadDocuments(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "aaa"})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "bbb"})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if _, err := ix.BuildAll(ctx); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "notes", "b.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	res, err := ix.BuildAll(ctx)
	if err != nil {
		t.Fatalf("BuildAll (gc): %v", err)
	}
	if res.Skipped != 1 || res.Removed != 1 {
		t.Fatalf("BuildAll result = %+v, want Skipped=1 (unchanged notes/a.md) Removed=1", res)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 || chunks[0].RefDocID != "notes/a" {
		t.Fatalf("unexpected chunks after gc: %+v", chunks)
	}
}

func TestBuildAllOnEmptyRootIsNoop(t *testing.T) {
	root := t.TempDir()
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})

	res, err := ix.BuildAll(context.Background())
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if res.Indexed != 0 || res.Removed != 0 {
		t.Fatalf("BuildAll result = %+v, want zero", res)
	}
}

func TestBuildAllOnMissingRootIsNoop(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})

	res, err := ix.BuildAll(context.Background())
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if res.Indexed != 0 || res.Removed != 0 {
		t.Fatalf("BuildAll result = %+v, want zero", res)
	}
}

func TestIndexDocumentMatchesFilesystemPathScheme(t *testing.T) {
	root := t.TempDir()
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	doc := connector.Document{ID: "42", Source: "github-myorg", Body: "issue body"}
	if err := ix.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	writeDoc(t, root, "github-myorg/42.md", doc)
	if err := ix.AddOrUpdateDocument(ctx, "github-myorg/42.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1 (API and filesystem paths should collide on the same doc)", len(chunks))
	}
}

func TestIndexDocumentRequiresSourceAndID(t *testing.T) {
	root := t.TempDir()
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.IndexDocument(ctx, connector.Document{ID: "x", Body: "b"}); err == nil {
		t.Fatal("expected error for missing source")
	}
	if err := ix.IndexDocument(ctx, connector.Document{Source: "s", Body: "b"}); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestIndexDocumentRoutesGoContentToCodeGraph(t *testing.T) {
	root := t.TempDir()
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(gs, nil, nil)
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	src := "package calc\n\nfunc Sum(a, b int) int {\n\treturn a + b\n}\n"
	doc := connector.Document{
		ID:          "owner/repo:contents:internal/calc/sum.go",
		Source:      "github-owner",
		Kind:        "content",
		Title:       "sum.go",
		Body:        src,
		Frontmatter: map[string]any{"path": "internal/calc/sum.go"},
	}
	if err := ix.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	entities, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	found := false
	for _, e := range entities {
		if e.Name == "Sum" && e.Type == "code-function" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a function entity from the deterministic code-graph path, got %+v", entities)
	}
}

func TestFrontmatterMetadataFlattensValues(t *testing.T) {
	fm := map[string]any{
		"project": "alpha",
		"nilval":  nil,
		"count":   7,
		"active":  true,
		"ratio":   1.5,
		"nested":  []string{"a", "b"},
	}
	got := frontmatterMetadata(fm)
	want := map[string]string{
		"project": "alpha",
		"nilval":  "",
		"count":   "7",
		"active":  "true",
		"ratio":   "1.5",
		"nested":  "[a b]",
	}
	if len(got) != len(want) {
		t.Fatalf("frontmatterMetadata len = %d, want %d (got %+v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("frontmatterMetadata[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestFrontmatterMetadataNilForEmptyMap(t *testing.T) {
	if got := frontmatterMetadata(nil); got != nil {
		t.Fatalf("frontmatterMetadata(nil) = %+v, want nil", got)
	}
	if got := frontmatterMetadata(map[string]any{}); got != nil {
		t.Fatalf("frontmatterMetadata({}) = %+v, want nil", got)
	}
}

func TestSanitizeID(t *testing.T) {
	clean := "plain-id_123.go"
	if got := sanitizeID(clean); got != clean {
		t.Fatalf("sanitizeID(%q) = %q, want unchanged clean id", clean, got)
	}
	if got := sanitizeID(""); got != "_" {
		t.Fatalf("sanitizeID(\"\") = %q, want _", got)
	}
	a := sanitizeID("a#b")
	b := sanitizeID("a_b")
	if a == b {
		t.Fatalf("sanitizeID collided: %q == %q", a, b)
	}
	if !strings.HasPrefix(a, "a_b-") || len(a) != len("a_b-")+8 {
		t.Fatalf("sanitizeID(a#b) = %q, want a_b-<8 hex>", a)
	}
}

func TestAddOrUpdateDocumentAbsolutePath(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Hello world."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	abs := filepath.Join(root, "notes", "doc1.md")
	if err := ix.AddOrUpdateDocument(ctx, abs); err != nil {
		t.Fatalf("AddOrUpdateDocument (abs): %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 || chunks[0].RefDocID != "notes/doc1" {
		t.Fatalf("chunks = %+v, want single notes/doc1 chunk", chunks)
	}
}

func TestAddOrUpdateDocumentBadFrontmatterFallsBackToBody(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "notes", "raw.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte("---\nbad: [unclosed\n---\nraw body text\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/raw.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	if chunks[0].Source != "notes" {
		t.Fatalf("unexpected source after fallback: %+v", chunks[0])
	}
	if !strings.HasPrefix(chunks[0].Text, "---\n") || !strings.Contains(chunks[0].Text, "raw body text") {
		t.Fatalf("unexpected chunk text after fallback (want raw content incl. frontmatter): %+v", chunks[0])
	}
}

func TestBuildAllSkipsDotDirsAndNonMarkdown(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "visible"})
	writeDoc(t, root, ".hidden/secret.md", connector.Document{ID: "secret", Source: ".hidden", Body: "hidden"})
	if err := os.WriteFile(filepath.Join(root, "notes", "readme.txt"), []byte("not markdown"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	res, err := ix.BuildAll(ctx)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if res.Indexed != 1 {
		t.Fatalf("Indexed = %d, want 1", res.Indexed)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 || chunks[0].RefDocID != "notes/doc1" {
		t.Fatalf("chunks = %+v, want only notes/doc1", chunks)
	}
}

func TestBuildAllGluesChatThreads(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC)
	writeDoc(t, root, "chat/m1.md", connector.Document{
		ID: "m1", Source: "chat", Kind: "message", Body: "deploy is broken?",
		UpdatedAt:   base,
		Frontmatter: map[string]any{"thread": "t1", "channel": "ops", "user": "alice"},
	})
	writeDoc(t, root, "chat/m2.md", connector.Document{
		ID: "m2", Source: "chat", Kind: "message", Body: "fixing it now.",
		UpdatedAt:   base.Add(2 * time.Minute),
		Frontmatter: map[string]any{"thread": "t1", "channel": "ops", "user": "bob"},
	})
	writeDoc(t, root, "chat/m3.md", connector.Document{
		ID: "m3", Source: "chat", Kind: "message", Body: "unrelated channel message",
		UpdatedAt: base.Add(3 * time.Minute),
	})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	res, err := ix.BuildAll(ctx)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if res.Indexed != 3 {
		t.Fatalf("Indexed = %d, want 3", res.Indexed)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2 (one glued thread + one standalone)", len(chunks))
	}
	var glued *vector.Chunk
	for i := range chunks {
		if strings.Contains(chunks[i].Text, "deploy is broken?") && strings.Contains(chunks[i].Text, "fixing it now.") {
			glued = &chunks[i]
		}
	}
	if glued == nil {
		t.Fatalf("reply chain not glued into one chunk: %+v", chunks)
	}
	if glued.RefDocID != "chat/m1" {
		t.Fatalf("thread chunk attributed to %q, want chat/m1 (earliest message)", glued.RefDocID)
	}
	if glued.Metadata["thread_id"] != "t1" {
		t.Fatalf("thread chunk metadata = %v, want thread_id=t1", glued.Metadata)
	}
	if !strings.Contains(glued.Text, "alice: deploy is broken?") || !strings.Contains(glued.Text, "bob: fixing it now.") {
		t.Fatalf("glued chunk must prefix each message with its speaker: %q", glued.Text)
	}
	for _, want := range []string{`"user":"alice"`, `"user":"bob"`} {
		if !strings.Contains(glued.Metadata["speakers"], want) {
			t.Fatalf("glued chunk speakers metadata missing %s: %v", want, glued.Metadata["speakers"])
		}
	}
}

func TestBulkReindexDropsStalePerMessageChunks(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC)
	writeDoc(t, root, "chat/m1.md", connector.Document{
		ID: "m1", Source: "chat", Kind: "message", Body: "deploy is broken?",
		UpdatedAt:   base,
		Frontmatter: map[string]any{"thread": "t1"},
	})
	writeDoc(t, root, "chat/m2.md", connector.Document{
		ID: "m2", Source: "chat", Kind: "message", Body: "fixing it now.",
		UpdatedAt:   base.Add(2 * time.Minute),
		Frontmatter: map[string]any{"thread": "t1"},
	})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	// Index each message individually first (the incremental per-document
	// path, which predates thread glueing).
	if err := ix.AddOrUpdateDocument(ctx, "chat/m1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument m1: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "chat/m2.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument m2: %v", err)
	}

	if _, err := ix.BuildAll(ctx); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1 (glued thread only)", len(chunks))
	}
	if chunks[0].RefDocID != "chat/m1" {
		t.Fatalf("thread chunk attributed to %q, want chat/m1", chunks[0].RefDocID)
	}
}

func TestBuildAllKeepsAPIFedDocuments(t *testing.T) {
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: t.TempDir(), Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	doc := connector.Document{
		ID: "issue-1", Source: "github", Kind: "note",
		Body: "api-fed content that never lands on disk",
	}
	if err := ix.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	if _, err := ix.BuildAll(ctx); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks after BuildAll = %d, want 1 (API-fed doc must survive GC)", len(chunks))
	}

	if err := ix.RemoveDocumentBySourceID(ctx, "github", "issue-1"); err != nil {
		t.Fatalf("RemoveDocumentBySourceID: %v", err)
	}
	if _, err := ix.BuildAll(ctx); err != nil {
		t.Fatalf("BuildAll after tombstone: %v", err)
	}
	chunks, err = vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("chunks after tombstone + BuildAll = %d, want 0", len(chunks))
	}
}

func TestBuildAllFromFreshProcessKeepsAPIFedDocuments(t *testing.T) {
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	root := t.TempDir()
	ctx := context.Background()

	// Simulate a sync --api run: the document is indexed but never lands on
	// disk, and the sync cursor already advanced.
	ix1 := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	if err := ix1.IndexDocument(ctx, connector.Document{
		ID: "issue-1", Source: "github", Kind: "note",
		Body: "api-fed content that never lands on disk",
	}); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	// Simulate `kb reindex` in a fresh process: a new Indexer with empty
	// in-memory apiRefs must still keep the API-fed document.
	ix2 := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	if _, err := ix2.BuildAll(ctx); err != nil {
		t.Fatalf("BuildAll from fresh process: %v", err)
	}
	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks after fresh-process BuildAll = %d, want 1 (API-fed doc must survive GC)", len(chunks))
	}
	if chunks[0].RefDocID != "github/issue-1" {
		t.Fatalf("chunk attributed to %q, want github/issue-1", chunks[0].RefDocID)
	}
}

func TestBuildAllGarbageCollectsFileSupersedingAPIFedDocument(t *testing.T) {
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	root := t.TempDir()
	ctx := context.Background()

	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	if err := ix.IndexDocument(ctx, connector.Document{
		ID: "issue-1", Source: "github", Kind: "note",
		Body: "api-fed content that never lands on disk",
	}); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	writeDoc(t, root, "github/issue-1.md", connector.Document{
		ID: "issue-1", Source: "github", Kind: "note",
		Body: "disk copy superseding the api-fed one",
	})
	if err := ix.AddOrUpdateDocument(ctx, "github/issue-1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	if err := os.Remove(filepath.Join(root, "github", "issue-1.md")); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if _, err := ix.BuildAll(ctx); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("chunks after file delete + BuildAll = %d, want 0 (stale apiRefs must not survive GC)", len(chunks))
	}
}

func TestIncrementalReindexRegluesThreadFromDisk(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC)
	writeDoc(t, root, "chat/m1.md", connector.Document{
		ID: "m1", Source: "chat", Kind: "message", Body: "deploy is broken?",
		UpdatedAt:   base,
		Frontmatter: map[string]any{"thread": "t1"},
	})
	writeDoc(t, root, "chat/m2.md", connector.Document{
		ID: "m2", Source: "chat", Kind: "message", Body: "fixing it now.",
		UpdatedAt:   base.Add(2 * time.Minute),
		Frontmatter: map[string]any{"thread": "t1"},
	})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if _, err := ix.BuildAll(ctx); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 || chunks[0].RefDocID != "chat/m1" {
		t.Fatalf("after BuildAll want one glued chunk under chat/m1, got %+v", chunks)
	}

	// Reindex a single thread member after a bulk glue: the whole thread
	// must be re-glued from disk, so the sibling's text survives and the
	// updated member's fresh text replaces its stale copy.
	writeDoc(t, root, "chat/m2.md", connector.Document{
		ID: "m2", Source: "chat", Kind: "message", Body: "fixing it now, ETA 10min.",
		UpdatedAt:   base.Add(3 * time.Minute),
		Frontmatter: map[string]any{"thread": "t1"},
	})
	if err := ix.AddOrUpdateDocument(ctx, "chat/m2.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument m2: %v", err)
	}

	chunks, err = vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks after incremental reindex = %d, want 1 (thread re-glued)", len(chunks))
	}
	if chunks[0].RefDocID != "chat/m1" {
		t.Fatalf("thread chunk attributed to %q, want chat/m1 (earliest message)", chunks[0].RefDocID)
	}
	if !strings.Contains(chunks[0].Text, "ETA 10min") || !strings.Contains(chunks[0].Text, "deploy is broken?") {
		t.Fatalf("re-glued chunk lost member text: %q", chunks[0].Text)
	}
}

func TestIndexDocumentRegluesThreadWithDiskSiblings(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC)
	writeDoc(t, root, "chat/m1.md", connector.Document{
		ID: "m1", Source: "chat", Kind: "message", Body: "deploy is broken?",
		UpdatedAt:   base,
		Frontmatter: map[string]any{"thread": "t1"},
	})
	writeDoc(t, root, "chat/m2.md", connector.Document{
		ID: "m2", Source: "chat", Kind: "message", Body: "fixing it now.",
		UpdatedAt:   base.Add(2 * time.Minute),
		Frontmatter: map[string]any{"thread": "t1"},
	})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if _, err := ix.BuildAll(ctx); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	// A server-side (API-fed) update to one member must re-glue the whole
	// thread, keeping the disk sibling's text in the index.
	if err := ix.IndexDocument(ctx, connector.Document{
		ID: "m2", Source: "chat", Kind: "message", Body: "fixing it now, ETA 10min.",
		UpdatedAt:   base.Add(3 * time.Minute),
		Frontmatter: map[string]any{"thread": "t1"},
	}); err != nil {
		t.Fatalf("IndexDocument m2: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks after API-fed update = %d, want 1 (thread re-glued)", len(chunks))
	}
	if chunks[0].RefDocID != "chat/m1" {
		t.Fatalf("thread chunk attributed to %q, want chat/m1 (earliest message)", chunks[0].RefDocID)
	}
	if !strings.Contains(chunks[0].Text, "ETA 10min") || !strings.Contains(chunks[0].Text, "deploy is broken?") {
		t.Fatalf("re-glued chunk lost member text: %q", chunks[0].Text)
	}
}

func TestIndexerConcurrentBulkAndDocumentOps(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC)
	writeDoc(t, root, "chat/m1.md", connector.Document{
		ID: "m1", Source: "chat", Kind: "message", Body: "deploy is broken?",
		UpdatedAt:   base,
		Frontmatter: map[string]any{"thread": "t1"},
	})
	writeDoc(t, root, "chat/m2.md", connector.Document{
		ID: "m2", Source: "chat", Kind: "message", Body: "fixing it now.",
		UpdatedAt:   base.Add(2 * time.Minute),
		Frontmatter: map[string]any{"thread": "t1"},
	})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	var n int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				id := "live-" + strconv.FormatInt(atomic.AddInt64(&n, 1), 10)
				_ = ix.IndexDocument(ctx, connector.Document{
					ID: id, Source: "chat", Kind: "message", Body: "live message " + id,
					UpdatedAt:   time.Now(),
					Frontmatter: map[string]any{"thread": "t1"},
				})
			}
		}
	}()

	for i := 0; i < 5; i++ {
		if _, err := ix.BuildAll(ctx); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("BuildAll: %v", err)
		}
	}
	close(stop)
	wg.Wait()

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks indexed after concurrent runs")
	}
}

func TestReindexRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("# secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if _, err := ix.Reindex(ctx, outside); err == nil {
		t.Fatalf("Reindex(%q) should reject paths outside root", outside)
	}
	if err := ix.AddOrUpdateDocument(ctx, outside); err == nil {
		t.Fatalf("AddOrUpdateDocument(%q) should reject paths outside root", outside)
	}
	if err := ix.AddOrUpdateDocument(ctx, filepath.Join("..", "outside.md")); err == nil {
		t.Fatal("AddOrUpdateDocument with .. escape should be rejected")
	}
	if err := ix.RemoveDocument(ctx, outside); err == nil {
		t.Fatalf("RemoveDocument(%q) should reject paths outside root", outside)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("indexed %d chunks from outside root, want 0", len(chunks))
	}
}

func TestIndexDocumentMessageUsesChatChunker(t *testing.T) {
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: t.TempDir(), Vector: vs, ChunkSize: 64})
	ctx := context.Background()

	long := strings.Repeat("sentence one. ", 80)
	doc := connector.Document{
		ID:        "m1",
		Source:    "telegram",
		Kind:      "message",
		Title:     "m1",
		Body:      long,
		UpdatedAt: time.Now().UTC(),
		Frontmatter: map[string]any{
			"thread": "root-1",
			"chat":   "c1",
		},
	}
	if err := ix.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("message chunked into %d chunks, want 1 (ChatChunker must not split a message)", len(chunks))
	}
	if chunks[0].Metadata["thread_id"] != "root-1" {
		t.Fatalf("thread_id metadata = %q, want root-1", chunks[0].Metadata["thread_id"])
	}
	if chunks[0].Metadata["chat"] != "c1" {
		t.Fatalf("frontmatter metadata lost: chat = %q, want c1", chunks[0].Metadata["chat"])
	}
	if chunks[0].Text != long {
		t.Fatalf("message text was altered: len(chunk) = %d, want %d", len(chunks[0].Text), len(long))
	}

	if err := ix.IndexDocument(ctx, connector.Document{
		ID:        "m2",
		Source:    "telegram",
		Kind:      "message",
		Body:      "short reply",
		UpdatedAt: time.Now().UTC(),
		Frontmatter: map[string]any{
			"thread": 42,
			"chat":   int64(99),
		},
	}); err != nil {
		t.Fatalf("IndexDocument m2: %v", err)
	}
	chunks, err = vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	for _, c := range chunks {
		if c.RefDocID == "telegram/m2" && c.Metadata["thread_id"] != "42" {
			t.Fatalf("numeric thread frontmatter = %q, want 42", c.Metadata["thread_id"])
		}
	}
}

func TestIndexerPruneSourceAndRemoveBySourceID(t *testing.T) {
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: t.TempDir(), Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := ix.IndexDocument(ctx, connector.Document{
			ID:     fmt.Sprintf("d%d", i),
			Source: "chat",
			Kind:   "message",
			Body:   "hello " + fmt.Sprint(i),
		}); err != nil {
			t.Fatalf("IndexDocument d%d: %v", i, err)
		}
	}
	if err := ix.IndexDocument(ctx, connector.Document{ID: "x1", Source: "notes", Body: "note"}); err != nil {
		t.Fatalf("IndexDocument notes/x1: %v", err)
	}

	removed, err := ix.PruneSource(ctx, "chat", map[string]struct{}{"d1": {}})
	if err != nil {
		t.Fatalf("PruneSource: %v", err)
	}
	if removed != 2 {
		t.Fatalf("PruneSource removed %d, want 2", removed)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range chunks {
		seen[c.RefDocID] = true
	}
	if seen["chat/d0"] || seen["chat/d2"] || !seen["chat/d1"] || !seen["notes/x1"] {
		t.Fatalf("post-prune refs = %v, want chat/d1 and notes/x1 only", seen)
	}

	if err := ix.RemoveDocumentBySourceID(ctx, "chat", "d1"); err != nil {
		t.Fatalf("RemoveDocumentBySourceID: %v", err)
	}
	chunks, err = vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) != 1 || chunks[0].RefDocID != "notes/x1" {
		t.Fatalf("after tombstone chunks = %+v, want notes/x1 only", chunks)
	}
}

func TestIndexerPruneSourceScopedToPrefixes(t *testing.T) {
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: t.TempDir(), Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	for _, id := range []string{"acme/widgets:contents:README.md", "acme/widgets:wiki:Home", "acme/widgets:issue:12"} {
		if err := ix.IndexDocument(ctx, connector.Document{ID: id, Source: "gh", Body: "body " + id}); err != nil {
			t.Fatalf("IndexDocument %s: %v", id, err)
		}
	}

	// Empty seen with a contents/wiki scope: only matching categories are
	// pruned; the incremental issue document must survive.
	removed, err := ix.PruneSource(ctx, "gh", map[string]struct{}{}, "acme/widgets:contents:", "acme/widgets:wiki:")
	if err != nil {
		t.Fatalf("PruneSource: %v", err)
	}
	if removed != 2 {
		t.Fatalf("PruneSource removed %d, want 2", removed)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	refs := map[string]bool{}
	for _, c := range chunks {
		refs[c.RefDocID] = true
	}
	wantRef := "gh/" + sanitizeID("acme/widgets:issue:12")
	if len(refs) != 1 || !refs[wantRef] {
		t.Fatalf("post-prune refs = %v, want only %s", refs, wantRef)
	}
}

func TestAddOrUpdateDocumentEmptyBodyIsNoopAndKeepsDoc(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: ""})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	ix := NewIndexer(Config{Root: root, Vector: vs, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument on empty body: %v", err)
	}

	chunks, err := vs.ChunksByDoc(ctx, "notes/doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("empty doc must produce no chunks, got %+v", chunks)
	}

	if _, ok, err := vs.DocHash(ctx, "notes/doc1"); err != nil || !ok {
		t.Fatalf("empty doc must still be recorded as processed, ok=%v err=%v", ok, err)
	}
}

func TestBlastRadiusReindexClearsStaleSupersededBy(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Alice knows Bob."})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Alice knows Carol."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	chat := &scriptedChat{responses: []string{
		aliceBobJSON, `{"title":"Alice Bob","summary":"AB"}`,
		aliceCarolJSON, `{"title":"Alice Carol","summary":"AC"}`,
		`{"entities":[{"name":"Carol","type":"person"},{"name":"Dan","type":"person"}],"relations":[{"source":"Carol","target":"Dan","type":"knows"}]}`,
		`{"title":"Carol Dan","summary":"CD"}`,
	}}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), graph.NewSummarizer(chat, "test-model"))
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}

	chunks, err := vs.ChunksByDoc(ctx, "notes/a")
	if err != nil {
		t.Fatalf("ChunksByDoc a: %v", err)
	}
	var aActive []vector.Chunk
	for _, c := range chunks {
		if c.ValidTo == "" {
			aActive = append(aActive, c)
		}
	}
	if len(aActive) != 1 || aActive[0].SupersededBy != "notes/b" {
		t.Fatalf("doc a active chunks = %+v, want exactly one superseded by notes/b", aActive)
	}

	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Carol knows Dan."})
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b (reindex): %v", err)
	}

	chunks, err = vs.ChunksByDoc(ctx, "notes/a")
	if err != nil {
		t.Fatalf("ChunksByDoc a (after reindex): %v", err)
	}
	for _, c := range chunks {
		if c.ValidTo == "" && c.SupersededBy != "" {
			t.Fatalf("doc a's active chunk still superseded by %q after b dropped the shared entity", c.SupersededBy)
		}
	}
}

func TestBlastRadiusReindexSupersedesOlderOverlappingDoc(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Alice knows Bob."})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Alice knows Carol."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	chat := &scriptedChat{responses: []string{
		aliceCarolJSON, `{"title":"Alice Carol","summary":"AC"}`,
		aliceBobJSON,
		aliceCarolJSON,
	}}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), graph.NewSummarizer(chat, "test-model"))
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}

	chunks, err := vs.ChunksByDoc(ctx, "notes/b")
	if err != nil {
		t.Fatalf("ChunksByDoc b: %v", err)
	}
	var bActive []vector.Chunk
	for _, c := range chunks {
		if c.ValidTo == "" {
			bActive = append(bActive, c)
		}
	}
	if len(bActive) != 1 {
		t.Fatalf("doc b active chunks = %+v, want exactly one", bActive)
	}
	if bActive[0].SupersededBy != "notes/a" {
		t.Fatalf("doc b chunk superseded_by = %q, want notes/a (a indexed later)", bActive[0].SupersededBy)
	}

	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Alice knows Carol!"})
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b (reindex): %v", err)
	}

	chunks, err = vs.ChunksByDoc(ctx, "notes/b")
	if err != nil {
		t.Fatalf("ChunksByDoc b (after reindex): %v", err)
	}
	bActive = bActive[:0]
	for _, c := range chunks {
		if c.ValidTo == "" {
			bActive = append(bActive, c)
		}
	}
	if len(bActive) != 1 {
		t.Fatalf("doc b active chunks after reindex = %+v, want exactly one", bActive)
	}
	if bActive[0].SupersededBy != "" {
		t.Fatalf("reindexed doc b chunk must not be superseded, got %q", bActive[0].SupersededBy)
	}

	chunks, err = vs.ChunksByDoc(ctx, "notes/a")
	if err != nil {
		t.Fatalf("ChunksByDoc a (after reindex): %v", err)
	}
	var aActive []vector.Chunk
	for _, c := range chunks {
		if c.ValidTo == "" {
			aActive = append(aActive, c)
		}
	}
	if len(aActive) != 1 || aActive[0].SupersededBy != "notes/b" {
		t.Fatalf("doc a active chunks after b reindex = %+v, want exactly one superseded by notes/b", aActive)
	}
}

func TestBlastRadiusReindexDoesNotMutuallySupersede(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Alice knows Bob."})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Alice knows Carol."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	chat := &scriptedChat{responses: []string{
		aliceBobJSON, `{"title":"Alice Bob","summary":"AB"}`,
		aliceCarolJSON, aliceDanJSON,
	}}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), graph.NewSummarizer(chat, "test-model"))
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}

	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Alice knows Dan."})
	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a (reindex): %v", err)
	}

	aChunks, err := vs.ChunksByDoc(ctx, "notes/a")
	if err != nil {
		t.Fatalf("ChunksByDoc a: %v", err)
	}
	var aActive []vector.Chunk
	for _, c := range aChunks {
		if c.ValidTo == "" {
			aActive = append(aActive, c)
		}
	}
	if len(aActive) != 1 || aActive[0].SupersededBy != "" {
		t.Fatalf("reindexed doc a active chunks = %+v, want exactly one with no superseded_by", aActive)
	}

	bChunks, err := vs.ChunksByDoc(ctx, "notes/b")
	if err != nil {
		t.Fatalf("ChunksByDoc b: %v", err)
	}
	var bActive []vector.Chunk
	for _, c := range bChunks {
		if c.ValidTo == "" {
			bActive = append(bActive, c)
		}
	}
	if len(bActive) != 1 || bActive[0].SupersededBy != "notes/a" {
		t.Fatalf("doc b active chunks = %+v, want exactly one superseded by notes/a", bActive)
	}
}

func TestBlastRadiusReindexClearsOwnMarkWhenOverlapGone(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Alice knows Bob."})
	writeDoc(t, root, "notes/b.md", connector.Document{ID: "b", Source: "notes", Body: "Alice knows Carol."})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	chat := &scriptedChat{responses: []string{
		aliceBobJSON, `{"title":"Alice Bob","summary":"AB"}`,
		aliceCarolJSON, `{"title":"Alice Carol","summary":"AC"}`,
		xavierYolandaJSON,
		`{"title":"Xavier Yolanda","summary":"XY"}`,
	}}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), graph.NewSummarizer(chat, "test-model"))
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "notes/b.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument b: %v", err)
	}

	writeDoc(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Body: "Xavier knows Yolanda."})
	if err := ix.AddOrUpdateDocument(ctx, "notes/a.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument a (reindex): %v", err)
	}

	chunks, err := vs.ChunksByDoc(ctx, "notes/a")
	if err != nil {
		t.Fatalf("ChunksByDoc a: %v", err)
	}
	for _, c := range chunks {
		if c.ValidTo == "" && c.SupersededBy != "" {
			t.Fatalf("reindexed doc a chunk still superseded by %q with no shared entity", c.SupersededBy)
		}
	}
}

func TestAddOrUpdateDocumentChainsMultipleVersionsInOrder(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Version one."})
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	embed := &fakeEmbedder{dim: 4}
	ix := NewIndexer(Config{Root: root, Vector: vs, Embed: embed, EmbedModel: "test-embed", ChunkSize: 512})
	ctx := context.Background()

	for i, body := range []string{"Version one.", "Version two.", "Version three."} {
		writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: body})
		if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
			t.Fatalf("AddOrUpdateDocument[%d]: %v", i, err)
		}
	}

	all, err := vs.ChunksByDoc(ctx, "notes/doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("chunks = %d, want 3 versions", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].Replaces != all[i-1].ID {
			t.Fatalf("version %d replaces %q, want %q (versions must come back in creation order)", i, all[i].Replaces, all[i-1].ID)
		}
	}
	if all[0].ValidTo == "" {
		t.Fatalf("first version must be closed, got %+v", all[0])
	}
	if all[len(all)-1].ValidTo != "" {
		t.Fatalf("last version must be active, got %+v", all[len(all)-1])
	}
}

func TestAddOrUpdateDocumentEmptyAfterNonemptyClosesAllVersions(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Alice knows Bob."})
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	chat := &scriptedChat{responses: []string{
		aliceBobJSON, `{"title":"Alice Bob","summary":"AB"}`,
	}}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), graph.NewSummarizer(chat, "test-model"))
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, Embed: &fakeEmbedder{dim: 4}, EmbedModel: "test-embed", ChunkSize: 512})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}
	writeDoc(t, root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: ""})
	if err := ix.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument (empty): %v", err)
	}

	all, err := vs.ChunksByDoc(ctx, "notes/doc1")
	if err != nil {
		t.Fatalf("ChunksByDoc: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("chunks = %+v, want exactly the single prior version (no new chunks for empty body)", all)
	}
	if all[0].ValidTo == "" {
		t.Fatalf("version must be closed after empty update, got %+v", all[0])
	}
	scored, err := vs.Query(ctx, []float32{1, 0, 0, 0}, 10, vector.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(scored) != 0 {
		t.Fatalf("Query = %+v, want none after empty update", scored)
	}
	entities, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("entities = %+v, want none (graph pruned)", entities)
	}
}

func TestBlastRadiusSkipsChatIdentityEntities(t *testing.T) {
	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	chat := &chatRoutingChat{
		decisions: []string{
			`{"entities":[{"name":"Postgres","type":"topic","description":"db"}],"relations":[{"source":"alice","target":"Postgres","type":"DECIDED"}]}`,
			`{"entities":[{"name":"Kubernetes","type":"topic","description":"k8s"}],"relations":[{"source":"alice","target":"Kubernetes","type":"DECIDED"}]}`,
		},
		summary: `{"title":"T","summary":"S"}`,
	}
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test-model"), graph.NewSummarizer(chat, "test-model"))
	updater = updater.WithChatExtractor(graph.NewChatExtractor(chat, "test-model"))
	ix := NewIndexer(Config{Root: t.TempDir(), Vector: vs, Graph: updater, ChunkSize: 512})
	ctx := context.Background()

	indexChat := func(id, body string) {
		t.Helper()
		if err := ix.IndexDocument(ctx, connector.Document{
			ID:        id,
			Source:    "chats",
			Kind:      "message",
			Body:      body,
			UpdatedAt: time.Now().UTC(),
			Frontmatter: map[string]any{
				"user": "alice",
				"ts":   time.Now().UTC().Format(time.RFC3339),
			},
		}); err != nil {
			t.Fatalf("IndexDocument %s: %v", id, err)
		}
	}
	indexChat("m1", "We decided to use Postgres for the new storage layer.")
	indexChat("m2", "We decided to use Kubernetes for the new cluster.")

	chunks, err := vs.ChunksByDoc(ctx, "chats/m1")
	if err != nil {
		t.Fatalf("ChunksByDoc chats/m1: %v", err)
	}
	for _, c := range chunks {
		if c.ValidTo == "" && c.SupersededBy != "" {
			t.Fatalf("chat chunk %s superseded by %q through a shared speaker", c.ID, c.SupersededBy)
		}
	}
}

func TestBlastRadiusSkipsCodePackageDocuments(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "code/calc.go", connector.Document{
		ID:     "code/calc.go",
		Source: "code",
		Kind:   "code",
		Title:  "calc.go",
		Body:   "package calc\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
	})
	writeDoc(t, root, "code/util.go", connector.Document{
		ID:     "code/util.go",
		Source: "code",
		Kind:   "code",
		Title:  "util.go",
		Body:   "package calc\n\nfunc Util() int {\n\treturn 42\n}\n",
	})

	db := openTestDB(t)
	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(gs, nil, nil)
	ix := NewIndexer(Config{Root: root, Vector: vs, Graph: updater, ChunkSize: 64})
	ctx := context.Background()

	if err := ix.AddOrUpdateDocument(ctx, "code/calc.go"); err != nil {
		t.Fatalf("AddOrUpdateDocument calc.go: %v", err)
	}
	if err := ix.AddOrUpdateDocument(ctx, "code/util.go"); err != nil {
		t.Fatalf("AddOrUpdateDocument util.go: %v", err)
	}

	chunks, err := vs.ChunksByDoc(ctx, "code/calc.go")
	if err != nil {
		t.Fatalf("ChunksByDoc code/calc.go: %v", err)
	}
	for _, c := range chunks {
		if c.ValidTo == "" && c.SupersededBy != "" {
			t.Fatalf("code chunk %s superseded by %q through a shared package symbol", c.ID, c.SupersededBy)
		}
	}
}

type chatRoutingChat struct {
	decisions []string
	summary   string
	i         int
}

func (c *chatRoutingChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	sys := ""
	if len(req.Messages) > 0 {
		sys = req.Messages[0].Content
	}
	if strings.Contains(sys, "DECIDED") {
		if c.i < len(c.decisions) {
			resp := c.decisions[c.i]
			c.i++
			return llm.ChatResponse{Content: resp}, nil
		}
		return llm.ChatResponse{Content: `{"entities":[],"relations":[]}`}, nil
	}
	return llm.ChatResponse{Content: c.summary}, nil
}
