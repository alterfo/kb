package integration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/importer"
	_ "github.com/alterfo/kb/internal/importer/jsonf"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/testkit"
	"github.com/alterfo/kb/internal/verify"
)

func TestFakeE2E_ImportIndexSearchAskGraphQuery(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	db, err := sqlite.Open(ctx, filepath.Join(root, "kb.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	fakeChat := testkit.NewFakeChat()
	fakeEmbed := testkit.NewFakeEmbedder()

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

	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(
		gs,
		graph.NewExtractor(fakeChat, "test"),
		graph.NewSummarizer(fakeChat, "test"),
	)
	idx := engine.NewIndexer(engine.Config{
		Root:         root,
		Vector:       vs,
		Graph:        updater,
		Embed:        fakeEmbed,
		EmbedModel:   "test",
		ChunkSize:    4096,
		ChunkOverlap: 512,
	})
	if err := idx.IndexDocument(ctx, docs[0]); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks indexed")
	}
	version, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	b := bm25.New()
	b.Rebuild(chunks, version)

	r := retriever.New(retriever.Config{
		Vector:         vs,
		BM25:           b,
		Chat:           fakeChat,
		Embed:          fakeEmbed,
		Graph:          gs,
		LLMModel:       "test",
		EmbedModel:     "test",
		Hybrid:         true,
		AuthorityBonus: map[string]float64{},
		RRFK:           60,
	})
	searchHits, err := r.Retrieve(ctx, "who maintains the retriever module", retriever.Options{K: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(searchHits) == 0 {
		t.Fatal("search returned no chunks")
	}

	orch := got.New(got.Config{
		Retriever:      retriever.Adapter{Retriever: r},
		Chat:           fakeChat,
		Model:          "test",
		K:              4,
		MaxSubgoals:    2,
		MaxConcurrency: 2,
	})
	tg := orch.Run(ctx, "what is the kb project and who maintains its retriever module")
	if strings.TrimSpace(tg.FinalAnswer) == "" {
		t.Fatal("ask produced an empty final answer")
	}
	if len(tg.Sources) == 0 {
		t.Fatal("ask produced no sources")
	}
	citationContext := make([]verify.Chunk, 0, len(tg.Sources))
	for _, src := range tg.Sources {
		citationContext = append(citationContext, verify.Chunk{
			FileName: src.FileName,
			FilePath: src.FilePath,
			ChunkID:  src.ChunkID,
		})
	}
	citationReport := verify.CheckCitations(tg.FinalAnswer, citationContext)
	if citationReport.HasMissing() {
		t.Fatalf("answer has missing citations: %+v", citationReport.Missing)
	}
	if len(citationReport.Citations) == 0 {
		t.Fatal("answer has no resolvable citations")
	}

	entities, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) == 0 {
		t.Fatal("knowledge graph has no entities after indexing")
	}
	relations, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	kbID := "kb-project-ee04970f"
	aliceID := "alice-person-869f76e5"
	wantGraph := verify.Graph{
		Entities: []graphstore.Entity{
			{ID: kbID, Name: "kb", Type: "project", Description: "a graph-based knowledge base"},
			{ID: aliceID, Name: "Alice", Type: "person", Description: "maintains the retriever module"},
		},
		Relations: []graphstore.Relation{
			{ID: "alice-person-869f76e5-maintains-kb-project-ee04970f-1105650a", Src: aliceID, Dst: kbID, Type: "maintains", Description: "Alice maintains the kb project", Weight: 1},
		},
	}
	graphDiff := verify.DiffGraph(verify.Graph{Entities: entities, Relations: relations}, wantGraph)
	if graphDiff.HasDifferences() {
		t.Fatalf("knowledge graph differs from golden graph: %+v", graphDiff)
	}
	matched, err := gs.MatchEntities(ctx, []string{entities[0].Name})
	if err != nil {
		t.Fatalf("MatchEntities: %v", err)
	}
	if len(matched) == 0 {
		t.Fatalf("MatchEntities(%q) returned nothing", entities[0].Name)
	}
	if _, _, err := gs.Neighbors(ctx, entities[0].ID, 2); err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if _, err := updater.RecomputeCommunities(ctx, []string{entities[0].ID}); err != nil {
		t.Fatalf("RecomputeCommunities: %v", err)
	}
	communities, err := gs.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	if len(communities) == 0 {
		t.Fatal("knowledge graph has no communities after recompute")
	}
	if _, err := gs.CommunitiesFor(ctx, []string{communities[0].ID}); err != nil {
		t.Fatalf("CommunitiesFor: %v", err)
	}
}
