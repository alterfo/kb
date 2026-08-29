package got

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
	"github.com/alterfo/kb/internal/verify"
)

type fakeContradictionDetector struct {
	report verify.ContradictionReport
	err    error
	calls  int32
	chunks []verify.Chunk
}

func (f *fakeContradictionDetector) Detect(ctx context.Context, query string, chunks []verify.Chunk) (verify.ContradictionReport, error) {
	atomic.AddInt32(&f.calls, 1)
	f.chunks = chunks
	return f.report, f.err
}

func contradictionPipeline() (Config, *fakeContradictionDetector) {
	retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{
		"sub1": goodChunks("sub1"),
	}}
	chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
		"You break a user question":         {Content: `["sub1"]`},
		"Given the original question":       {Content: `[]`},
		"You combine sub-answers":           {Content: "final answer"},
		"You answer a focused sub-question": {Content: "sub answer"},
	}}
	detector := &fakeContradictionDetector{report: verify.ContradictionReport{
		Contradictions: []verify.Contradiction{{ChunkA: "c1", ChunkB: "c2", Reason: "conflicting redactions"}},
	}}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	cfg.ContradictionDetector = detector
	return cfg, detector
}

func TestContradictionDetectionOffByDefault(t *testing.T) {
	cfg, detector := contradictionPipeline()
	g := New(cfg).Run(context.Background(), "q")

	if atomic.LoadInt32(&detector.calls) != 0 {
		t.Fatalf("detector called %d times with flag off, want 0", detector.calls)
	}
	if g.FinalAnswer != "final answer" {
		t.Fatalf("got FinalAnswer %q", g.FinalAnswer)
	}
	if len(g.Sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(g.Sources))
	}
	for _, n := range g.Nodes {
		if len(n.Contradictions) != 0 {
			t.Fatalf("flag off must not attach contradictions, node %+v", n)
		}
	}
}

func TestContradictionDetectionEnabledFlagsOnNode(t *testing.T) {
	cfg, detector := contradictionPipeline()
	cfg.DetectContradictions = true
	g := New(cfg).Run(context.Background(), "q")

	if atomic.LoadInt32(&detector.calls) != 1 {
		t.Fatalf("detector calls = %d, want 1", detector.calls)
	}
	if len(detector.chunks) != 2 || detector.chunks[0].FileName != "sub1-1.md" {
		t.Fatalf("detector chunks = %+v, want sub1 chunks", detector.chunks)
	}
	if g.FinalAnswer != "final answer" {
		t.Fatalf("got FinalAnswer %q", g.FinalAnswer)
	}
	found := false
	for _, n := range g.Nodes {
		if n.Type != NodeSubgoal {
			continue
		}
		found = true
		if len(n.Contradictions) != 1 || n.Contradictions[0] != "c1 <-> c2: conflicting redactions" {
			t.Fatalf("node contradictions = %+v", n.Contradictions)
		}
	}
	if !found {
		t.Fatal("no subgoal node produced")
	}
}

func TestContradictionDetectionErrorFailsOpen(t *testing.T) {
	cfg, detector := contradictionPipeline()
	detector.err = errors.New("boom")
	detector.report = verify.ContradictionReport{}
	cfg.DetectContradictions = true
	g := New(cfg).Run(context.Background(), "q")

	if atomic.LoadInt32(&detector.calls) != 1 {
		t.Fatalf("detector calls = %d, want 1", detector.calls)
	}
	if g.FinalAnswer != "final answer" {
		t.Fatalf("got FinalAnswer %q, want pipeline to complete fail-open", g.FinalAnswer)
	}
	for _, n := range g.Nodes {
		if n.Type == NodeSubgoal && len(n.Contradictions) != 0 {
			t.Fatalf("detector error must not block, node %+v", n)
		}
	}
}

func TestToVerifyChunks(t *testing.T) {
	chunks := []vector.ScoredChunk{{
		Chunk: vector.Chunk{ID: "c1", FileName: "a.md", FilePath: "notes/a.md", Text: "text"},
	}}
	got := toVerifyChunks(chunks)
	if len(got) != 1 || got[0].ChunkID != "c1" || got[0].FileName != "a.md" ||
		got[0].FilePath != "notes/a.md" || got[0].Text != "text" {
		t.Fatalf("got %+v", got)
	}
}
