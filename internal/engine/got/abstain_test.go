package got

import (
	"context"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/store/vector"
)

func abstainCorpusRetriever(byQuery map[string][]vector.ScoredChunk) *filterCapturingRetriever {
	return &filterCapturingRetriever{fakeRetriever: fakeRetriever{byQuery: byQuery}}
}

func highScoreChunks() []vector.ScoredChunk {
	return []vector.ScoredChunk{{Chunk: vector.Chunk{ID: "c1", RefDocID: "d1", Text: "relevant"}, Score: 0.9}}
}

func TestAbstainWhenAllSubgoalsUncovered(t *testing.T) {
	cap := abstainCorpusRetriever(map[string][]vector.ScoredChunk{})
	o := New(Config{
		Retriever:        cap,
		K:                4,
		AbstainThreshold: 0.5,
		Sleep:            instantSleep,
	})
	g := o.Run(context.Background(), "unknown topic")
	if g.FinalAnswer != abstainFinalAnswer {
		t.Fatalf("FinalAnswer = %q, want abstention verdict", g.FinalAnswer)
	}
	if !strings.Contains(strings.ToLower(g.FinalAnswer), "does not contain") {
		t.Errorf("verdict should explicitly acknowledge absence: %q", g.FinalAnswer)
	}
}

func TestNoAbstainWhenAnySubgoalCovered(t *testing.T) {
	cap := abstainCorpusRetriever(map[string][]vector.ScoredChunk{
		"known": highScoreChunks(),
	})
	o := New(Config{
		Retriever:        cap,
		K:                4,
		AbstainThreshold: 0.5,
		Sleep:            instantSleep,
	})
	g := o.Run(context.Background(), "known")
	if g.FinalAnswer == abstainFinalAnswer {
		t.Fatal("covered subgoal must prevent full abstention")
	}
	if strings.TrimSpace(g.FinalAnswer) == "" {
		t.Fatal("expected non-empty draft answer")
	}
}

func TestNoAbstainWhenDisabled(t *testing.T) {
	cap := abstainCorpusRetriever(map[string][]vector.ScoredChunk{})
	o := New(Config{Retriever: cap, K: 4, Sleep: instantSleep})
	g := o.Run(context.Background(), "unknown topic")
	if g.FinalAnswer == abstainFinalAnswer {
		t.Fatal("default config must keep legacy behavior (no abstention)")
	}
}

func TestAbstainThresholdBoundaryRespected(t *testing.T) {
	chunks := highScoreChunks()
	half := []vector.ScoredChunk{chunks[0]}
	half[0].Score = 0.25

	cap := abstainCorpusRetriever(map[string][]vector.ScoredChunk{"q": half})
	o := New(Config{
		Retriever:        cap,
		K:                2,
		AbstainThreshold: 0.1,
		Sleep:            instantSleep,
	})
	g := o.Run(context.Background(), "q")
	if g.FinalAnswer == abstainFinalAnswer {
		t.Fatal("coverage above threshold must not abstain")
	}
}

func TestSynthesisPromptsKeepMarkersAndProtocol(t *testing.T) {
	if !strings.HasPrefix(synthesizeSystemPrompt, "You answer a focused sub-question using ONLY the provided excerpts.") {
		t.Fatal("synthesize prompt marker prefix changed; testkit matching would break")
	}
	if !strings.HasPrefix(aggregateSystemPrompt, "You combine sub-answers into one coherent, grounded answer") {
		t.Fatal("aggregate prompt marker prefix changed; testkit matching would break")
	}
	for _, p := range []string{synthesizeSystemPrompt, aggregateSystemPrompt} {
		if !strings.Contains(p, "exact numbers") {
			t.Errorf("prompt missing strict answer protocol: %q", p[:60])
		}
	}
}
