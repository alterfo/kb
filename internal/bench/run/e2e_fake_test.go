package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/bench/corpus"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/sqlite"
	"github.com/alterfo/kb/internal/testkit"
)

func TestFakeE2E_BenchPipelineEndToEnd(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	db, err := sqlite.Open(ctx, filepath.Join(root, "kb.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	fakeChat := testkit.NewFakeChat()
	fakeEmbed := testkit.NewFakeEmbedder()

	docs, warns, err := corpus.LoadCorpus(filepath.Join("..", "corpus", "testdata", "txt-corpus"))
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(warns) != 0 || len(docs) != 2 {
		t.Fatalf("corpus docs=%d warns=%v", len(docs), warns)
	}

	vs := sqlite.NewVectorStore(db)
	gs := sqlite.NewGraphStore(db)
	updater := graph.NewGraphUpdater(gs, graph.NewExtractor(fakeChat, "test"), graph.NewSummarizer(fakeChat, "test"))
	idx := engine.NewIndexer(engine.Config{
		Vector:       vs,
		Graph:        updater,
		Embed:        fakeEmbed,
		EmbedModel:   "test",
		ChunkSize:    4096,
		ChunkOverlap: 512,
	})
	for _, d := range docs {
		if err := idx.IndexDocument(ctx, d.ToDocument()); err != nil {
			t.Fatalf("IndexDocument(%s): %v", d.ID, err)
		}
	}

	chunks, err := vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	version, err := db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	bidx := bm25.New()
	bidx.Rebuild(chunks, version)

	r := retriever.New(retriever.Config{
		Vector:     vs,
		BM25:       bidx,
		Chat:       fakeChat,
		Embed:      fakeEmbed,
		Graph:      gs,
		LLMModel:   "test",
		EmbedModel: "test",
		Hybrid:     true,
	})
	orch := got.New(got.Config{
		Retriever:      retriever.Adapter{Retriever: r},
		Chat:           fakeChat,
		Model:          "test",
		K:              4,
		MaxSubgoals:    2,
		MaxConcurrency: 2,
	})

	out := filepath.Join(root, "answers.jsonl")
	questions, warns, err := corpus.LoadQuestions(filepath.Join("..", "corpus", "testdata", "questions-sample.jsonl"))
	if err != nil {
		t.Fatalf("LoadQuestions: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("question warnings = %v", warns)
	}

	runner := &Runner{
		Questions: questions,
		OutPath:   out,
		Ask: func(ctx context.Context, q corpus.Question) (string, []string) {
			g := orch.Run(ctx, q.Text)
			docIDs := make([]string, 0, len(g.Sources))
			for _, s := range g.Sources {
				if s.DocID != "" {
					docIDs = append(docIDs, s.DocID)
				}
			}
			return g.FinalAnswer, docIDs
		},
	}

	rep, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read answers: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("answers lines = %d, want 3", len(lines))
	}
	for i, line := range lines {
		var a Answer
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			t.Fatalf("line %d malformed: %v", i, err)
		}
		if a.QuestionID == "" || a.Answer == "" {
			t.Fatalf("line %d incomplete: %+v", i, a)
		}
	}
	if rep.Total != 3 {
		t.Fatalf("report total = %d, want 3", rep.Total)
	}
	if rep.Types["info_not_found"] == nil {
		t.Fatal("per-type stats missing info_not_found")
	}
}
