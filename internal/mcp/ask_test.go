package mcp

import (
	"context"
	"testing"

	"github.com/alterfo/kb/internal/connector"
)

func TestAsk_FailOpenWithoutLLMStillReturnsAnswer(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	writeDoc(t, te.root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "Paris is the capital of France."})
	if err := te.indexer.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	_, out, err := te.server.ask(ctx, nil, askIn{Query: "capital France"})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if out.Answer == "" {
		t.Fatalf("ask: got empty answer, want a fail-open fallback")
	}
}

func TestAsk_EmptyCorpusStillReturnsSomeAnswer(t *testing.T) {
	te := newTestEnv(t, nil)
	_, out, err := te.server.ask(context.Background(), nil, askIn{Query: "anything"})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if out.Answer == "" {
		t.Fatalf("ask: got empty answer on empty corpus, want fail-open placeholder text")
	}
	if len(out.Sources) != 0 {
		t.Fatalf("ask: sources = %+v, want none on empty corpus", out.Sources)
	}
}
