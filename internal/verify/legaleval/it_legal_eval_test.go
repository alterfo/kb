//go:build integration

package legaleval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/importer/legalru"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/store/vector"
	"github.com/alterfo/kb/internal/verify"
)

// minRate reads a float threshold from envVar, falling back to def when
// unset or unparseable. Env-overridable so CI or a local run against a
// weaker/stronger model can tune the bar without editing the test.
func minRate(envVar string, def float64) float64 {
	v := os.Getenv(envVar)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

type expectedGraph struct {
	Entities []struct {
		ID string `json:"id"`
	} `json:"entities"`
	Relations []struct {
		Src  string `json:"src"`
		Dst  string `json:"dst"`
		Type string `json:"type"`
	} `json:"relations"`
}

func loadExpectedGraph(t *testing.T, path string) expectedGraph {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read expected graph: %v", err)
	}
	var g expectedGraph
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatalf("unmarshal expected graph: %v", err)
	}
	return g
}

// graphRecall reports what fraction of expected entities/relations are
// present in got. Relations are matched by (src, dst, type) since IDs are
// generated at index time; entities are matched by exact ID, which the
// deterministic legal-article/legal-amendment parsing in
// internal/graph/updater.go produces stably (only the plenum "interprets"
// relations depend on the LLM's judgment of which article a point
// clarifies, so relation recall is expected to be noisier than entity
// recall).
func graphRecall(want expectedGraph, gotEntities []graphstore.Entity, gotRelations []graphstore.Relation) (entityRecall, relationRecall float64) {
	gotEntityIDs := make(map[string]bool, len(gotEntities))
	for _, e := range gotEntities {
		gotEntityIDs[e.ID] = true
	}
	entityHits := 0
	for _, e := range want.Entities {
		if gotEntityIDs[e.ID] {
			entityHits++
		}
	}
	if len(want.Entities) > 0 {
		entityRecall = float64(entityHits) / float64(len(want.Entities))
	}

	type relKey struct{ src, dst, typ string }
	gotRelKeys := make(map[relKey]bool, len(gotRelations))
	for _, r := range gotRelations {
		gotRelKeys[relKey{r.Src, r.Dst, r.Type}] = true
	}
	relHits := 0
	for _, r := range want.Relations {
		// want.Src/Dst are entity IDs from the fixture; got relations key
		// on the same deterministic IDs for legal-article/legal-amendment
		// nodes, so a direct match works without re-deriving IDs.
		if gotRelKeys[relKey{r.Src, r.Dst, r.Type}] {
			relHits++
		}
	}
	if len(want.Relations) > 0 {
		relationRecall = float64(relHits) / float64(len(want.Relations))
	}
	return entityRecall, relationRecall
}

func requireLiveLLM(t *testing.T) {
	t.Helper()
	if os.Getenv("KB_LLM_IT") != "1" {
		t.Skip("KB_LLM_IT != 1, skipping live-LLM integration test")
	}
}

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

type e2eRetrieverAdapter struct{ r *retriever.Retriever }

func (a e2eRetrieverAdapter) RetrieveMode(ctx context.Context, query string, k int, mode retriever.Mode) ([]vector.ScoredChunk, error) {
	return a.r.Retrieve(ctx, query, retriever.Options{K: k, Mode: mode})
}

func TestIntegration_LegalEvalHarness(t *testing.T) {
	requireLiveLLM(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	env := config.Defaults()
	client := liveClient(t)

	goldDir := filepath.Join("..", "..", "..", "internal", "importer", "legalru", "testdata", "gold")
	codePath := filepath.Join(goldDir, "gk-rf-part1.md")
	plenumPath := filepath.Join(goldDir, "plenum-25-2015.md")
	qaPath := filepath.Join(goldDir, "qa_pairs.json")

	pairs, err := LoadQAPairs(qaPath)
	if err != nil {
		t.Fatalf("LoadQAPairs: %v", err)
	}
	if len(pairs) == 0 {
		t.Fatal("no qa pairs loaded")
	}
	corpus, err := LoadCorpus(codePath)
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	plenumPoints, err := LoadPlenumPoints(plenumPath, "вс-рф/пленум/пост-25")
	if err != nil {
		t.Fatalf("LoadPlenumPoints: %v", err)
	}

	root := t.TempDir()
	db, err := sqlite.Open(ctx, filepath.Join(root, "kb.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(client, env.LLMModel), graph.NewSummarizer(client, env.LLMModel)).
		WithLegalExtractor(graph.NewLegalExtractor(client, env.LLMModel))
	idx := engine.NewIndexer(engine.Config{
		Root:         root,
		Vector:       vs,
		Graph:        updater,
		Embed:        client,
		EmbedModel:   env.EmbedModel,
		ChunkSize:    env.ChunkSize,
		ChunkOverlap: env.ChunkOverlap,
	})

	docs, err := legalru.New().Import(codePath)
	if err != nil {
		t.Fatalf("legalru.Import: %v", err)
	}
	for _, d := range docs {
		d.Source = "legal"
		if err := idx.IndexDocument(ctx, d); err != nil {
			t.Fatalf("IndexDocument %s: %v", d.ID, err)
		}
	}
	plenumRaw, err := os.ReadFile(plenumPath)
	if err != nil {
		t.Fatalf("ReadFile plenum: %v", err)
	}
	plenumDoc := connector.Document{
		ID:     "вс-рф/пленум/пост-25",
		Kind:   "legal-plenum",
		Title:  "Постановление Пленума ВС РФ N 25",
		Body:   string(plenumRaw),
		Source: "legal",
		Frontmatter: map[string]any{
			"kind": "legal-plenum",
			"id":   "вс-рф/пленум/пост-25",
		},
	}
	if err := idx.IndexDocument(ctx, plenumDoc); err != nil {
		t.Fatalf("IndexDocument plenum: %v", err)
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	byChunk := map[string]string{}
	for _, c := range chunks {
		if id := c.Metadata["id"]; id != "" {
			byChunk[c.ID] = id
		}
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
	orch := got.New(got.Config{
		Retriever:      e2eRetrieverAdapter{r: r},
		Chat:           client,
		Model:          env.LLMModel,
		K:              4,
		MaxSubgoals:    2,
		MaxConcurrency: 2,
	})

	plenum := NewPlenum(plenumPoints)
	eval := &Eval{
		Ask: func(ctx context.Context, q string) (Answer, error) {
			tg := orch.Run(ctx, q)
			ctxChunks := make([]verify.Chunk, 0, len(tg.Sources))
			for _, s := range tg.Sources {
				ctxChunks = append(ctxChunks, verify.Chunk{FileName: s.FileName, FilePath: s.FilePath, ChunkID: s.ChunkID})
			}
			// NHSR must measure the citations actually present in the
			// answer, not the retrieved evidence: parse the answer's
			// (file) citations and keep only those resolvable to the
			// retrieved context, plus the unresolvable ones so they count
			// against the rate as hallucinated.
			rep := verify.CheckCitations(tg.FinalAnswer, ctxChunks)
			var cits []Citation
			for _, c := range rep.Citations {
				cits = append(cits, Citation{FileName: c.FileName, ChunkID: c.ChunkID})
			}
			seen := map[string]bool{}
			for _, m := range rep.Missing {
				if seen[m.Raw] {
					continue
				}
				seen[m.Raw] = true
				cits = append(cits, Citation{FileName: m.Raw})
			}
			return Answer{Text: tg.FinalAnswer, Citations: cits}, nil
		},
		Judge:  &LLMJudge{Chat: client, Model: env.LLMModel},
		Corpus: corpus,
		Plenum: plenum,
		Resolve: func(ctx context.Context, c Citation) (string, bool) {
			if c.ChunkID != "" {
				if id, ok := byChunk[c.ChunkID]; ok {
					return id, true
				}
			}
			return corpus.Resolve(c)
		},
	}

	rep, err := eval.Run(ctx, pairs)
	if err != nil {
		t.Fatalf("eval.Run: %v", err)
	}
	if len(rep.Results) != len(pairs) {
		t.Fatalf("results = %d, want %d", len(rep.Results), len(pairs))
	}
	if rep.AskErrors != 0 {
		t.Fatalf("ask errors: %d", rep.AskErrors)
	}
	if rep.NHSR.Total == 0 {
		t.Fatal("no statute citations resolved; harness cannot measure Non-Hallucinated-Statute-Rate")
	}
	t.Logf("legal eval report:\n%s", rep.Summary())
	if minNHSR := minRate("KB_LEGALEVAL_MIN_NHSR", 0.5); rep.NHSR.Rate() < minNHSR {
		t.Errorf("NHSR = %.3f (%d/%d), want >= %.3f (override with KB_LEGALEVAL_MIN_NHSR)",
			rep.NHSR.Rate(), rep.NHSR.Passed, rep.NHSR.Total, minNHSR)
	}

	// expected_graph.json was previously unused by any test. It mixes two
	// kinds of graph structure: legal-article entities, which
	// GraphUpdater.canonicalizePlenumContribution/retargetPlenumInterprets
	// build deterministically from "статья N" references (see
	// internal/graph/updater.go), and legal-amendment entities/"amends"
	// relations from Redaction parsing, which as of this writing are not
	// wired into the graph store at all (no "amends" relation type is ever
	// produced). So entity recall is asserted as a real regression check;
	// relation recall is only logged, since it's capped well below 1.0 by
	// design (5 of 13 expected relations are "amends") and the rest depend
	// on the LLM correctly resolving which article a Plenum point discusses.
	gotEntities, err := gs.AllEntities(ctx)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	gotRelations, err := gs.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	wantGraph := loadExpectedGraph(t, filepath.Join(goldDir, "expected_graph.json"))
	entityRecall, relationRecall := graphRecall(wantGraph, gotEntities, gotRelations)
	t.Logf("graph recall vs expected_graph.json: entities=%.3f relations=%.3f (relations informational only, see comment)", entityRecall, relationRecall)
	if minEntityRecall := minRate("KB_LEGALEVAL_MIN_ENTITY_RECALL", 0.4); entityRecall < minEntityRecall {
		t.Errorf("entity recall vs expected_graph.json = %.3f, want >= %.3f (override with KB_LEGALEVAL_MIN_ENTITY_RECALL)", entityRecall, minEntityRecall)
	}
}
