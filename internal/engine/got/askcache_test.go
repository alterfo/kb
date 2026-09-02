package got

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

type fakeAskCache struct {
	entries  map[string]ThoughtGraph
	getCalls int
	putCalls int
}

func newFakeAskCache() *fakeAskCache {
	return &fakeAskCache{entries: map[string]ThoughtGraph{}}
}

func (f *fakeAskCache) Get(ctx context.Context, query string) (ThoughtGraph, bool, error) {
	f.getCalls++
	g, ok := f.entries[query]
	return g, ok, nil
}

func (f *fakeAskCache) Put(ctx context.Context, query string, g ThoughtGraph) error {
	f.putCalls++
	f.entries[query] = g
	return nil
}

func TestRunCacheHitSkipsPipeline(t *testing.T) {
	var chatCalls int
	var retrieverCalls int
	cache := newFakeAskCache()
	cached := ThoughtGraph{Query: "cached", FinalAnswer: "from cache"}
	cache.entries["cached"] = cached

	cfg := baseConfig()
	cfg.Chat = fakeChat{resp: llm.ChatResponse{Content: `["sub1"]`}, calls: &chatCalls}
	cfg.Retriever = fakeRetriever{calls: &retrieverCalls}
	cfg.AskCache = cache

	g := New(cfg).Run(context.Background(), "cached")

	if g.FinalAnswer != "from cache" {
		t.Fatalf("FinalAnswer = %q, want cached answer", g.FinalAnswer)
	}
	if chatCalls != 0 || retrieverCalls != 0 {
		t.Fatalf("cache hit still ran pipeline: chat=%d retriever=%d", chatCalls, retrieverCalls)
	}
	if cache.getCalls != 1 {
		t.Fatalf("Get calls = %d, want 1", cache.getCalls)
	}
}

func TestRunCacheMissPopulatesAndHitsSecondTime(t *testing.T) {
	retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{
		"sub1": goodChunks("sub1"),
	}}
	chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
		"You break a user question":   {Content: `["sub1"]`},
		"Given the original question": {Content: `[]`},
		"You combine sub-answers":     {Content: "final aggregated answer"},
	}}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	cfg.AskCache = newFakeAskCache()
	o := New(cfg)

	first := o.Run(context.Background(), "question")
	if first.FinalAnswer != "final aggregated answer" {
		t.Fatalf("first FinalAnswer = %q", first.FinalAnswer)
	}

	cached, ok, err := cfg.AskCache.(*fakeAskCache).Get(context.Background(), "question")
	if err != nil || !ok {
		t.Fatalf("cache entry after first run = (%v, %v)", ok, err)
	}
	if cached.FinalAnswer != "final aggregated answer" {
		t.Fatalf("cached FinalAnswer = %q", cached.FinalAnswer)
	}

	// A second run must be served from cache without re-running decompose,
	// which would need the scripted chat again.
	second := o.Run(context.Background(), "question")
	if second.FinalAnswer != "final aggregated answer" {
		t.Fatalf("second FinalAnswer = %q", second.FinalAnswer)
	}
	if cfg.AskCache.(*fakeAskCache).putCalls != 1 {
		t.Fatalf("Put calls = %d, want 1", cfg.AskCache.(*fakeAskCache).putCalls)
	}
}

func TestRunCachesFailOpenAnswer(t *testing.T) {
	cache := newFakeAskCache()
	cfg := baseConfig()
	cfg.AskCache = cache
	// No Retriever and no Chat: Run degrades to a deterministic fail-open
	// placeholder, which must still be cached so the next ask does not
	// re-run the degraded pipeline.
	o := New(cfg)

	first := o.Run(context.Background(), "q")
	if first.FinalAnswer == "" {
		t.Fatal("fail-open run returned empty answer")
	}
	if cache.putCalls != 1 {
		t.Fatalf("Put calls = %d, want 1 (fail-open answers are cached)", cache.putCalls)
	}
	second := o.Run(context.Background(), "q")
	if second.FinalAnswer != first.FinalAnswer {
		t.Fatalf("second fail-open answer = %q, want cached %q", second.FinalAnswer, first.FinalAnswer)
	}
	if cache.putCalls != 1 {
		t.Fatalf("Put calls after second run = %d, want 1 (served from cache)", cache.putCalls)
	}
}
