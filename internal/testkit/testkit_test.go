package testkit

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestFakeEmbedderDeterministic(t *testing.T) {
	fake := NewFakeEmbedder()
	texts := []string{"The kb project is a graph knowledge base.", "Alice maintains the retriever module."}

	first, err := fake.Embed(context.Background(), "test", texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	second, err := fake.Embed(context.Background(), "test", texts)
	if err != nil {
		t.Fatalf("Embed again: %v", err)
	}
	if len(first) != len(texts) || len(second) != len(texts) {
		t.Fatalf("got %d/%d vectors, want %d", len(first), len(second), len(texts))
	}
	for i := range texts {
		if len(first[i]) != DefaultDim || len(second[i]) != DefaultDim {
			t.Fatalf("vector %d dim = %d/%d, want %d", i, len(first[i]), len(second[i]), DefaultDim)
		}
		if !equalVec(first[i], second[i]) {
			t.Fatalf("vector %d differs across identical calls", i)
		}
		norm := vecNorm(first[i])
		if math.Abs(norm-1) > 1e-6 {
			t.Fatalf("vector %d norm = %.6f, want 1", i, norm)
		}
	}
	if equalVec(first[0], first[1]) {
		t.Fatal("distinct texts produced identical vectors")
	}
}

func TestFakeEmbedderEmptyAndError(t *testing.T) {
	fake := NewFakeEmbedder()
	vecs, err := fake.Embed(context.Background(), "test", []string{""})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || !allZero(vecs[0]) {
		t.Fatalf("empty text = %v, want single zero vector", vecs)
	}

	boom := FakeEmbedder{Err: context.DeadlineExceeded}
	if _, err := boom.Embed(context.Background(), "test", []string{"x"}); err == nil {
		t.Fatal("expected injected error")
	}
}

func TestFakeChatDeterministic(t *testing.T) {
	fake := NewFakeChat()
	req := llm.ChatRequest{
		Model: "test",
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You extract a knowledge graph from a text chunk."},
			{Role: "user", Content: "Alice maintains the kb project."},
		},
	}
	first, err := fake.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	second, err := fake.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat again: %v", err)
	}
	if first.Content != second.Content {
		t.Fatalf("Chat content differs across identical calls: %q vs %q", first.Content, second.Content)
	}
	if strings.TrimSpace(first.Content) == "" {
		t.Fatal("Chat returned empty content")
	}
}

func TestFakeChatDefaultExtractionJSON(t *testing.T) {
	fake := NewFakeChat()
	resp, err := fake.Chat(context.Background(), llm.ChatRequest{
		Model: "test",
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You extract a knowledge graph from a text chunk."},
			{Role: "user", Content: "Alice maintains the kb project."},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var parsed struct {
		Entities []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"entities"`
		Relations []struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Type   string `json:"type"`
		} `json:"relations"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		t.Fatalf("extraction JSON invalid: %v", err)
	}
	if len(parsed.Entities) == 0 || len(parsed.Relations) == 0 {
		t.Fatalf("extraction JSON empty: %s", resp.Content)
	}
}

func TestFakeChatDefaultDecomposeJSON(t *testing.T) {
	fake := NewFakeChat()
	resp, err := fake.Chat(context.Background(), llm.ChatRequest{
		Model: "test",
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You break a user question into 2-5 focused sub-questions."},
			{Role: "user", Content: "what is the kb project"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var parsed []struct {
		Subquestion string `json:"subquestion"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		t.Fatalf("decompose JSON invalid: %v", err)
	}
	if len(parsed) == 0 {
		t.Fatalf("decompose JSON empty: %s", resp.Content)
	}
}

func TestGraphExtractorAndSummarizerUseFakeChat(t *testing.T) {
	fake := NewFakeChat()

	ext := graph.NewExtractor(fake, "test")
	extraction, err := ext.ExtractChunk(context.Background(), "Alice maintains the kb project.")
	if err != nil {
		t.Fatalf("ExtractChunk: %v", err)
	}
	if len(extraction.Entities) == 0 {
		t.Fatal("ExtractChunk returned no entities")
	}

	sum := graph.NewSummarizer(fake, "test")
	title, summary := sum.Summarize(context.Background(), []graphstore.Entity{{Name: "kb", Type: "project"}}, nil)
	if title == "" || summary == "" {
		t.Fatalf("Summarize = (%q, %q), want non-empty", title, summary)
	}
}

type stubRetriever struct {
	chunks []vector.ScoredChunk
}

func (s stubRetriever) RetrieveMode(ctx context.Context, query string, k int, mode retriever.Mode) ([]vector.ScoredChunk, error) {
	return s.chunks, nil
}

func TestFakeChatDrivesGraphOfThoughts(t *testing.T) {
	fake := NewFakeChat()
	orch := got.New(got.Config{
		Retriever: stubRetriever{chunks: []vector.ScoredChunk{
			{Chunk: vector.Chunk{ID: "sample#0", FileName: "sample.md", FilePath: "sample/sample.md", Text: "The kb project is a graph knowledge base."}},
		}},
		Chat:           fake,
		Model:          "test",
		K:              2,
		MaxSubgoals:    2,
		MaxConcurrency: 2,
	})
	tg := orch.Run(context.Background(), "what is the kb project")
	if strings.TrimSpace(tg.FinalAnswer) == "" {
		t.Fatal("GoT produced an empty final answer")
	}
	if len(tg.Sources) == 0 {
		t.Fatal("GoT produced no sources")
	}
	if !strings.Contains(tg.FinalAnswer, "(sample.md)") {
		t.Fatalf("GoT final answer missing generated citation: %q", tg.FinalAnswer)
	}
}

func TestFakeChatErrPropagates(t *testing.T) {
	boom := FakeChat{Err: context.DeadlineExceeded}
	if _, err := boom.Chat(context.Background(), llm.ChatRequest{Model: "test"}); err == nil {
		t.Fatal("expected injected error to propagate")
	}
}

func TestFakeChatFallbackWhenNoResponseMatches(t *testing.T) {
	fake := FakeChat{Fallback: "fallback answer"}
	resp, err := fake.Chat(context.Background(), llm.ChatRequest{
		Model: "test",
		Messages: []llm.ChatMessage{
			{Role: "user", Content: "a prompt that matches no marker"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "fallback answer" {
		t.Fatalf("fallback = %q, want %q", resp.Content, "fallback answer")
	}
}

func TestFakeEmbedderCustomAndZeroDim(t *testing.T) {
	custom := FakeEmbedder{Dim: 8}
	vecs, err := custom.Embed(context.Background(), "test", []string{"kb"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 8 {
		t.Fatalf("custom dim = %d, want 8", len(vecs[0]))
	}

	zero := FakeEmbedder{Dim: 0}
	vecs, err = zero.Embed(context.Background(), "test", []string{"kb"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != DefaultDim {
		t.Fatalf("zero dim = %d, want %d", len(vecs[0]), DefaultDim)
	}
}

func TestCitationsAnswerWithoutNames(t *testing.T) {
	if got := citationsAnswer("prefix", nil); got != "prefix" {
		t.Fatalf("citationsAnswer(prefix, nil) = %q, want prefix", got)
	}
}

func equalVec(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func allZero(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
}

func vecNorm(v []float32) float64 {
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	return math.Sqrt(sumSq)
}
