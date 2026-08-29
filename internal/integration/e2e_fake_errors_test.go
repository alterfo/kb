package integration

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/importer"
	_ "github.com/alterfo/kb/internal/importer/jsonf"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/testkit"
	"github.com/alterfo/kb/internal/verify"
)

// fakePipeline wires the same import->index->retrieve stack as
// TestFakeE2E_ImportIndexSearchAskGraphQuery, but parameterized on the
// FakeChat/FakeEmbedder so error-injection tests can swap in a faulty
// double for exactly the layer under test.
type fakePipeline struct {
	root    string
	db      *sqlite.DB
	vs      *sqlite.VectorStore
	gs      *sqlite.GraphStore
	updater *graph.GraphUpdater
	idx     *engine.Indexer
	chat    testkit.FakeChat
	embed   testkit.FakeEmbedder
}

func newFakePipeline(t *testing.T, chat testkit.FakeChat, embed testkit.FakeEmbedder) *fakePipeline {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := sqlite.Open(ctx, filepath.Join(root, "kb.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(chat, "test"), graph.NewSummarizer(chat, "test"))
	idx := engine.NewIndexer(engine.Config{
		Root:         root,
		Vector:       vs,
		Graph:        updater,
		Embed:        embed,
		EmbedModel:   "test",
		ChunkSize:    4096,
		ChunkOverlap: 512,
	})
	return &fakePipeline{root: root, db: db, vs: vs, gs: gs, updater: updater, idx: idx, chat: chat, embed: embed}
}

// sampleDoc loads the same fixture the golden-path fake e2e uses.
func sampleDoc(t *testing.T) connector.Document {
	t.Helper()
	imp, err := importer.New(".json")
	if err != nil {
		t.Fatalf("importer.New: %v", err)
	}
	docs, err := imp.Import(filepath.Join("testdata", "sample.json"))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("Import returned %d docs, want 1", len(docs))
	}
	docs[0].Source = "sample"
	docs[0].ID = "sample"
	return docs[0]
}

// retrieverFor rebuilds BM25 from whatever is currently in the vector store
// and returns a hybrid retriever wired to the pipeline's stores.
func (p *fakePipeline) retrieverFor(t *testing.T) *retriever.Retriever {
	t.Helper()
	ctx := context.Background()
	chunks, err := p.vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	version, err := p.db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	b := bm25.New()
	b.Rebuild(chunks, version)
	return retriever.New(retriever.Config{
		Vector:         p.vs,
		BM25:           b,
		Chat:           p.chat,
		Embed:          p.embed,
		Graph:          p.gs,
		LLMModel:       "test",
		EmbedModel:     "test",
		Hybrid:         true,
		AuthorityBonus: map[string]float64{},
		RRFK:           60,
	})
}

var errFakeBoom = errors.New("fake: injected failure")

// TestFakeE2E_MalformedExtractorJSON documents the extractor's fail-open
// contract (internal/graph/extract.go): a canned LLM response that isn't
// valid JSON must not fail indexing, but it must also not silently invent a
// graph. If this ever regresses to fail-closed (IndexDocument errors) or to
// hallucinated entities (non-empty graph from garbage input), this test
// catches it.
func TestFakeE2E_MalformedExtractorJSON(t *testing.T) {
	ctx := context.Background()
	chat := testkit.FakeChat{Responses: map[string]string{}, Fallback: "{not valid json"}
	embed := testkit.NewFakeEmbedder()
	p := newFakePipeline(t, chat, embed)

	doc := sampleDoc(t)
	if err := p.idx.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("IndexDocument should fail open on malformed extractor JSON, got error: %v", err)
	}

	entities, err := p.gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("expected no entities from malformed extraction, got %d: %+v", len(entities), entities)
	}

	chunks, err := p.vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("indexing should still store chunks even when graph extraction yields nothing")
	}
}

// TestFakeE2E_EmbedderFailure documents that an embedder outage
// (internal/engine/indexer.go's fail-open embed branch) never blocks
// indexing: the document is still searchable through the BM25 leg even
// though no vectors were produced.
func TestFakeE2E_EmbedderFailure(t *testing.T) {
	ctx := context.Background()
	chat := testkit.NewFakeChat()
	embed := testkit.FakeEmbedder{Err: errFakeBoom}
	p := newFakePipeline(t, chat, embed)

	doc := sampleDoc(t)
	if err := p.idx.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("IndexDocument should fail open on embedder error, got error: %v", err)
	}

	r := p.retrieverFor(t)
	hits, err := r.Retrieve(ctx, "retriever module", retriever.Options{K: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("BM25 leg should still surface the document when the embedder is down")
	}
}

// TestFakeE2E_AskLLMFailure documents the GoT orchestrator's fail-open
// contract (internal/engine/got/orchestrator.go): Run never returns an
// error and never panics, even when every chat call fails; it must still
// produce a non-empty degraded FinalAnswer.
func TestFakeE2E_AskLLMFailure(t *testing.T) {
	ctx := context.Background()
	chat := testkit.NewFakeChat()
	embed := testkit.NewFakeEmbedder()
	p := newFakePipeline(t, chat, embed)

	doc := sampleDoc(t)
	if err := p.idx.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}
	r := p.retrieverFor(t)

	errChat := testkit.FakeChat{Err: errFakeBoom}
	orch := got.New(got.Config{
		Retriever:      retriever.Adapter{Retriever: r},
		Chat:           errChat,
		Model:          "test",
		K:              4,
		MaxSubgoals:    2,
		MaxConcurrency: 2,
	})
	tg := orch.Run(ctx, "what is the kb project and who maintains its retriever module")
	if strings.TrimSpace(tg.FinalAnswer) == "" {
		t.Fatal("orchestrator should still produce a degraded, non-empty answer when the LLM is unavailable")
	}
}

// TestFakeE2E_MissingCitationDetected is the mirror image of the
// golden-path assertion in e2e_fake_test.go: it proves verify.CheckCitations
// actually flags a citation that does not resolve to any retrieved source,
// rather than always reporting clean.
func TestFakeE2E_MissingCitationDetected(t *testing.T) {
	ctx := context.Background()
	chat := testkit.NewFakeChat()
	// aggregateMarker's canned response is overridden to cite a file that
	// was never part of the retrieved context.
	chat.Responses["combine sub-answers into one coherent"] = "The provided sources answer the question. (ghost.md)"
	embed := testkit.NewFakeEmbedder()
	p := newFakePipeline(t, chat, embed)

	doc := sampleDoc(t)
	if err := p.idx.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}
	r := p.retrieverFor(t)

	orch := got.New(got.Config{
		Retriever:      retriever.Adapter{Retriever: r},
		Chat:           chat,
		Model:          "test",
		K:              4,
		MaxSubgoals:    2,
		MaxConcurrency: 2,
	})
	tg := orch.Run(ctx, "what is the kb project and who maintains its retriever module")
	if !strings.Contains(tg.FinalAnswer, "ghost.md") {
		t.Fatalf("expected the injected ghost.md citation in the final answer, got: %q", tg.FinalAnswer)
	}

	citationContext := make([]verify.Chunk, 0, len(tg.Sources))
	for _, src := range tg.Sources {
		citationContext = append(citationContext, verify.Chunk{
			FileName: src.FileName,
			FilePath: src.FilePath,
			ChunkID:  src.ChunkID,
		})
	}
	report := verify.CheckCitations(tg.FinalAnswer, citationContext)
	if !report.HasMissing() {
		t.Fatal("CheckCitations should flag (ghost.md) as a missing citation")
	}
}

// TestFakeE2E_EmptyCorpus documents that retrieving from and asking against
// an empty corpus is safe: no panic, no error, and a degraded but non-empty
// answer from the orchestrator.
func TestFakeE2E_EmptyCorpus(t *testing.T) {
	ctx := context.Background()
	chat := testkit.NewFakeChat()
	embed := testkit.NewFakeEmbedder()
	p := newFakePipeline(t, chat, embed)

	r := p.retrieverFor(t)
	hits, err := r.Retrieve(ctx, "who maintains the retriever module", retriever.Options{K: 5})
	if err != nil {
		t.Fatalf("Retrieve on empty corpus should not error, got: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Retrieve on empty corpus should return no hits, got %d", len(hits))
	}

	orch := got.New(got.Config{
		Retriever:      retriever.Adapter{Retriever: r},
		Chat:           chat,
		Model:          "test",
		K:              4,
		MaxSubgoals:    2,
		MaxConcurrency: 2,
	})
	tg := orch.Run(ctx, "who maintains the retriever module")
	if strings.TrimSpace(tg.FinalAnswer) == "" {
		t.Fatal("orchestrator should still produce a degraded, non-empty answer against an empty corpus")
	}
}
