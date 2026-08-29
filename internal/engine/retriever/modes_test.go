package retriever

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

// scriptedChat routes responses by the system prompt prefix, letting a
// single fake cover partial answers and the reduce step in one test.
type scriptedChat struct {
	byPrompt map[string]llm.ChatResponse
	fallback llm.ChatResponse
	mu       sync.Mutex
	calls    int
}

func (s *scriptedChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	sys := ""
	if len(req.Messages) > 0 {
		sys = req.Messages[0].Content
	}
	for prefix, resp := range s.byPrompt {
		if strings.HasPrefix(sys, prefix) {
			return resp, nil
		}
	}
	return s.fallback, nil
}

func TestRetrieveGlobalMapReduce(t *testing.T) {
	roots := []graphstore.Community{
		{ID: "c1", Level: 1, Title: "t1", Summary: "summary one", SourceChunks: []string{"a"}},
		{ID: "c2", Level: 1, Title: "t2", Summary: "summary two"},
		{ID: "c0", Level: 0, Title: "t0", Summary: "fine-grained summary"},
	}
	chat := &scriptedChat{byPrompt: map[string]llm.ChatResponse{
		"You are answering a user question with the help": {Content: "partial c1"},
		"You synthesize a final answer":                   {Content: "reduced answer"},
	}}
	r := New(Config{
		Graph:    &fakeGraphStore{communities: roots},
		Chat:     chat,
		LLMModel: "model",
		DefaultK: 10,
	})

	got, err := r.Retrieve(context.Background(), "what are the main themes", Options{K: 10, Mode: ModeGlobal})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) == 0 || got[0].Chunk.ID != "global:answer" {
		t.Fatalf("expected global answer chunk first, got %+v", got)
	}
	if got[0].Chunk.Text != "reduced answer" {
		t.Fatalf("answer text = %q, want reduced answer", got[0].Chunk.Text)
	}
	found := map[string]bool{}
	for _, sc := range got {
		found[sc.Chunk.ID] = true
	}
	if !found["community:c1"] || !found["community:c2"] {
		t.Fatalf("expected both root community chunks, got %v", found)
	}
	if found["community:c0"] {
		t.Fatalf("level-0 community must not appear in root map-reduce, got %v", found)
	}
	if chat.calls != 3 {
		t.Fatalf("chat calls = %d, want 3 (2 partials + 1 reduce)", chat.calls)
	}
}

func TestRetrieveGlobalReduceFailureFailsOpenToSummaries(t *testing.T) {
	roots := []graphstore.Community{
		{ID: "c1", Level: 0, Title: "t1", Summary: "summary one"},
	}
	chat := &scriptedChat{byPrompt: map[string]llm.ChatResponse{
		"You are answering a user question with the help": {Content: "partial"},
		"You synthesize a final answer":                   {Content: ""},
	}}
	r := New(Config{Graph: &fakeGraphStore{communities: roots}, Chat: chat, LLMModel: "model", DefaultK: 10})

	got, err := r.Retrieve(context.Background(), "themes", Options{K: 10, Mode: ModeGlobal})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "community:c1" {
		t.Fatalf("expected community summary chunks only, got %+v", got)
	}
}

func TestRetrieveGlobalDegradesToLocal(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	t.Run("no communities", func(t *testing.T) {
		r := New(Config{
			Vector: vs,
			BM25:   idx,
			Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
			Chat:   &scriptedChat{},
			Hybrid: true,
			Graph:  &fakeGraphStore{},
		})
		got, err := r.Retrieve(context.Background(), "apple", Options{K: 10, Mode: ModeGlobal})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if len(got) == 0 || got[0].Chunk.ID != "a" {
			t.Fatalf("expected local results on empty hierarchy, got %+v", got)
		}
	})

	t.Run("no chat", func(t *testing.T) {
		r := New(Config{
			Vector: vs,
			BM25:   idx,
			Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
			Hybrid: true,
			Graph:  &fakeGraphStore{communities: []graphstore.Community{{ID: "c1", Level: 0, Summary: "s"}}},
		})
		got, err := r.Retrieve(context.Background(), "apple", Options{K: 10, Mode: ModeGlobal})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if len(got) == 0 || got[0].Chunk.ID != "a" {
			t.Fatalf("expected local results without chat, got %+v", got)
		}
	})

	t.Run("graph error", func(t *testing.T) {
		r := New(Config{
			Vector: vs,
			BM25:   idx,
			Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
			Chat:   &scriptedChat{},
			Hybrid: true,
			Graph:  &fakeGraphStore{allErr: errors.New("boom")},
		})
		got, err := r.Retrieve(context.Background(), "apple", Options{K: 10, Mode: ModeGlobal})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if len(got) == 0 || got[0].Chunk.ID != "a" {
			t.Fatalf("expected local results on graph error, got %+v", got)
		}
	})
}

func TestRetrieveDriftSeedsCommunityThenRefinesLocally(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
		{ID: "b", RefDocID: "doc-b", Text: "banana grove", FilePath: "notes/b.md", Embedding: []float32{0, 1}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	gs := &fakeGraphStore{communities: []graphstore.Community{
		{ID: "apple-comm", Level: 0, Title: "apple cluster", Summary: "everything about apples", SourceChunks: []string{"a"}},
		{ID: "banana-comm", Level: 0, Title: "banana cluster", Summary: "everything about bananas", SourceChunks: []string{"b"}},
	}}
	embedder := fakeEmbedder{vec: func(text string) []float32 {
		if strings.Contains(text, "apple") {
			return []float32{1, 0}
		}
		if strings.Contains(text, "banana") {
			return []float32{0, 1}
		}
		return []float32{0, 0}
	}}

	r := New(Config{
		Vector:     vs,
		BM25:       idx,
		Embed:      embedder,
		Hybrid:     true,
		Graph:      gs,
		EmbedModel: "model",
		DefaultK:   10,
		CandidateK: 20,
	})

	got, err := r.Retrieve(context.Background(), "how are apples grown", Options{K: 10, Mode: ModeDrift})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected drift results")
	}
	ids := map[string]bool{}
	for _, sc := range got {
		ids[sc.Chunk.ID] = true
	}
	if !ids["community:apple-comm"] {
		t.Fatalf("expected community seed chunk in drift results, got %v", ids)
	}
	if ids["community:banana-comm"] {
		t.Fatalf("banana community must not seed apple query, got %v", ids)
	}
	if !ids["a"] {
		t.Fatalf("expected local refine to include member chunk a, got %v", ids)
	}
}

func TestRetrieveDriftDegradesToLocal(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	t.Run("no embedder", func(t *testing.T) {
		r := New(Config{
			Vector: vs,
			BM25:   idx,
			Hybrid: true,
			Graph:  &fakeGraphStore{communities: []graphstore.Community{{ID: "c1", Level: 0, Summary: "s"}}},
		})
		got, err := r.Retrieve(context.Background(), "apple", Options{K: 10, Mode: ModeDrift})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if len(got) == 0 || got[0].Chunk.ID != "a" {
			t.Fatalf("expected local results without embedder, got %+v", got)
		}
	})

	t.Run("no summarized communities", func(t *testing.T) {
		r := New(Config{
			Vector: vs,
			BM25:   idx,
			Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
			Hybrid: true,
			Graph:  &fakeGraphStore{communities: []graphstore.Community{{ID: "c1", Level: 0, Summary: ""}}},
		})
		got, err := r.Retrieve(context.Background(), "apple", Options{K: 10, Mode: ModeDrift})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		if len(got) == 0 || got[0].Chunk.ID != "a" {
			t.Fatalf("expected local results without summaries, got %+v", got)
		}
	})
}
