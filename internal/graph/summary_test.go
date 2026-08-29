package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
)

func testMembers() ([]graphstore.Entity, []graphstore.Relation) {
	entities := []graphstore.Entity{
		{ID: "a", Name: "Alice", Type: "person"},
		{ID: "b", Name: "Bob", Type: "person"},
	}
	relations := []graphstore.Relation{
		{ID: "ab", Src: "a", Dst: "b", Type: "knows", Weight: 1},
	}
	return entities, relations
}

func TestSummarizeValidJSON(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{"title":"Team Alice-Bob","summary":"Alice and Bob collaborate."}`}}
	s := NewSummarizer(chat, "model")

	entities, relations := testMembers()
	title, summary := s.Summarize(context.Background(), entities, relations)
	if title != "Team Alice-Bob" {
		t.Fatalf("title = %q", title)
	}
	if summary != "Alice and Bob collaborate." {
		t.Fatalf("summary = %q", summary)
	}
}

func TestSummarizeCodeFencedJSON(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: "```json\n{\"title\":\"T\",\"summary\":\"S\"}\n```"}}
	s := NewSummarizer(chat, "model")

	entities, relations := testMembers()
	title, summary := s.Summarize(context.Background(), entities, relations)
	if title != "T" || summary != "S" {
		t.Fatalf("got title=%q summary=%q", title, summary)
	}
}

func TestSummarizeBrokenJSONFallsBackToMemberNames(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: "not json"}}
	s := NewSummarizer(chat, "model")

	entities, relations := testMembers()
	title, summary := s.Summarize(context.Background(), entities, relations)
	if title != "Alice, Bob" {
		t.Fatalf("title = %q, want fallback 'Alice, Bob'", title)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty on fail-open", summary)
	}
}

func TestSummarizeTransportErrorFallsBack(t *testing.T) {
	chat := &fakeChat{err: errors.New("boom")}
	s := NewSummarizer(chat, "model")

	entities, relations := testMembers()
	title, _ := s.Summarize(context.Background(), entities, relations)
	if title != "Alice, Bob" {
		t.Fatalf("title = %q, want fallback", title)
	}
}

func TestSummarizeNilChatFallsBack(t *testing.T) {
	s := NewSummarizer(nil, "model")

	entities, relations := testMembers()
	title, summary := s.Summarize(context.Background(), entities, relations)
	if title != "Alice, Bob" || summary != "" {
		t.Fatalf("got title=%q summary=%q, want fallback", title, summary)
	}
}

func TestSummarizeEmptyEntities(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{"title":"T","summary":"S"}`}}
	s := NewSummarizer(chat, "model")

	title, summary := s.Summarize(context.Background(), nil, nil)
	if title != "" || summary != "" {
		t.Fatalf("got title=%q summary=%q, want empty for no members", title, summary)
	}
}

func TestSummarizeEmptyTitleFallsBackToMemberNames(t *testing.T) {
	chat := &fakeChat{resp: llm.ChatResponse{Content: `{"title":"","summary":"S"}`}}
	s := NewSummarizer(chat, "model")

	entities, relations := testMembers()
	title, summary := s.Summarize(context.Background(), entities, relations)
	if title != "Alice, Bob" {
		t.Fatalf("title = %q, want fallback", title)
	}
	if summary != "S" {
		t.Fatalf("summary = %q, want 'S'", summary)
	}
}
