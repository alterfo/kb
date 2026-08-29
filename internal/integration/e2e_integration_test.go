//go:build integration

package integration

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/importer"
	_ "github.com/alterfo/kb/internal/importer/jsonf"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
	"github.com/alterfo/kb/internal/verify"
)

func liveClient(t *testing.T) *llm.Client {
	t.Helper()
	env := config.Defaults()
	if v := os.Getenv("KB_LLM_BASE_URL"); v != "" {
		env.LLMBaseURL = v
	}
	return llm.NewClient(llm.Config{
		BaseURL:           env.LLMBaseURL,
		NoProxyHosts:      env.NoProxy,
		DefaultEmbedModel: env.EmbedModel,
		RequestTimeout:    5 * time.Minute,
	})
}

func requireLiveLLM(t *testing.T) {
	t.Helper()
	if os.Getenv("KB_LLM_IT") != "1" {
		t.Skip("KB_LLM_IT != 1, skipping live-LLM integration test")
	}
}

func TestIntegration_RealEmbedChatEntityExtract(t *testing.T) {
	requireLiveLLM(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env := config.Defaults()
	client := liveClient(t)

	dim, err := client.Dim(ctx)
	if err != nil {
		t.Fatalf("Dim: %v", err)
	}
	if dim <= 0 {
		t.Fatalf("Dim = %d, want > 0", dim)
	}

	texts := []string{"The kb project is a graph knowledge base.", "Привет, мир"}
	vecs, err := client.Embed(ctx, env.EmbedModel, texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("Embed returned %d vectors, want %d", len(vecs), len(texts))
	}
	for i, v := range vecs {
		if len(v) != dim {
			t.Fatalf("vector %d has dim %d, want %d", i, len(v), dim)
		}
		var sumSq float64
		for _, x := range v {
			sumSq += float64(x) * float64(x)
		}
		norm := math.Sqrt(sumSq)
		if norm < 0.99 || norm > 1.01 {
			t.Errorf("vector %d norm = %.4f, want ~1 (normalized)", i, norm)
		}
	}

	resp, err := client.Chat(ctx, llm.ChatRequest{
		Model:    env.LLMModel,
		Messages: []llm.ChatMessage{{Role: "user", Content: "Reply with exactly: OK"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if strings.TrimSpace(resp.Content) == "" {
		t.Fatal("Chat returned empty content")
	}

	extractor := graph.NewExtractor(client, env.LLMModel)
	ext, err := extractor.ExtractChunk(ctx, "Acme Corp develops the Go language. Alice works at Acme on the retriever module.")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(ext.Entities) == 0 {
		t.Fatal("ExtractChunk returned no entities on a live model")
	}
}

func TestIntegration_MiniE2E_ImportIndexSearchAskGraphQuery(t *testing.T) {
	requireLiveLLM(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := config.Defaults()
	client := liveClient(t)

	root := t.TempDir()
	sample := filepath.Join(root, "sample.json")
	body := `{
  "title": "Retriever module",
  "description": "The kb project is a graph-based knowledge base. The retriever module fuses dense vectors and BM25 scores. Alice maintains the retriever module and Bob wrote the indexer.",
  "team": "retrieval"
}`
	if err := os.WriteFile(sample, []byte(body), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	imp, err := importer.New(".json")
	if err != nil {
		t.Fatalf("importer.New: %v", err)
	}
	docs, err := imp.Import(sample)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("Import returned %d docs, want 1", len(docs))
	}
	docs[0].Source = "sample"

	db, err := sqlite.Open(ctx, filepath.Join(root, "kb.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(client, env.LLMModel), graph.NewSummarizer(client, env.LLMModel))
	idx := engine.NewIndexer(engine.Config{
		Root:         root,
		Vector:       vs,
		Graph:        updater,
		Embed:        client,
		EmbedModel:   env.EmbedModel,
		ChunkSize:    env.ChunkSize,
		ChunkOverlap: env.ChunkOverlap,
	})
	for _, d := range docs {
		if err := idx.IndexDocument(ctx, d); err != nil {
			t.Fatalf("IndexDocument: %v", err)
		}
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
		Chat:           client,
		Embed:          client,
		Graph:          gs,
		LLMModel:       env.LLMModel,
		EmbedModel:     env.EmbedModel,
		Hybrid:         true,
		AuthorityBonus: env.AuthorityBonus,
		RRFK:           env.RRFK,
	})
	searchHits, err := r.Retrieve(ctx, "who maintains the retriever module", retriever.Options{K: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(searchHits) == 0 {
		t.Fatal("search returned no chunks")
	}

	orch := got.New(got.Config{
		Retriever:      e2eRetrieverAdapter{r: r},
		Chat:           client,
		Model:          env.LLMModel,
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
	// Soft quality check: on a real model output is non-deterministic, so
	// this doesn't demand an exact match (unlike the fake e2e's golden-graph
	// diff) — it only demands the answer actually engages with the fixture
	// content instead of degrading to a generic fail-open placeholder, and
	// that at least one citation the model produced resolves to a real
	// retrieved source rather than being fabricated.
	lowerAnswer := strings.ToLower(tg.FinalAnswer)
	if !strings.Contains(lowerAnswer, "alice") && !strings.Contains(lowerAnswer, "kb") && !strings.Contains(lowerAnswer, "retriever") {
		t.Errorf("final answer does not mention any expected entity (alice/kb/retriever): %q", tg.FinalAnswer)
	}
	citationContext := make([]verify.Chunk, 0, len(tg.Sources))
	for _, src := range tg.Sources {
		citationContext = append(citationContext, verify.Chunk{
			FileName: src.FileName,
			FilePath: src.FilePath,
			ChunkID:  src.ChunkID,
		})
	}
	if report := verify.CheckCitations(tg.FinalAnswer, citationContext); len(report.Citations) == 0 {
		t.Errorf("expected at least one citation in the answer to resolve to a retrieved source, got none (missing: %+v)", report.Missing)
	}

	entities, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) == 0 {
		t.Fatal("knowledge graph has no entities after indexing")
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
	communities, err := gs.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	if len(communities) > 0 {
		if _, err := gs.CommunitiesFor(ctx, []string{communities[0].ID}); err != nil {
			t.Fatalf("CommunitiesFor: %v", err)
		}
	}
}

type e2eRetrieverAdapter struct{ r *retriever.Retriever }

func (a e2eRetrieverAdapter) RetrieveMode(ctx context.Context, query string, k int, mode retriever.Mode) ([]vector.ScoredChunk, error) {
	return a.r.Retrieve(ctx, query, retriever.Options{K: k, Mode: mode})
}
