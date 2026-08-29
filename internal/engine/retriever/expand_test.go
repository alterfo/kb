package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/llm"
)

type fakeChat struct {
	resp llm.ChatResponse
	err  error
}

func (f fakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return f.resp, f.err
}

func TestExpandQueryNilChatFailsOpen(t *testing.T) {
	got := expandQuery(context.Background(), nil, "model", "original query")
	if len(got) != 1 || got[0] != "original query" {
		t.Fatalf("got %v, want [original query]", got)
	}
}

func TestExpandQueryEmptyQuery(t *testing.T) {
	got := expandQuery(context.Background(), fakeChat{}, "model", "")
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("got %v, want ['']", got)
	}
}

func TestExpandQueryChatErrorFailsOpen(t *testing.T) {
	chat := fakeChat{err: errors.New("boom")}
	got := expandQuery(context.Background(), chat, "model", "q")
	if len(got) != 1 || got[0] != "q" {
		t.Fatalf("got %v, want [q]", got)
	}
}

func TestExpandQueryParsesJSONArray(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: `["sub one", "sub two", "sub three"]`}}
	got := expandQuery(context.Background(), chat, "model", "q")
	want := []string{"sub one", "sub two", "sub three"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestExpandQueryParsesCodeFencedJSON(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: "```json\n[\"a\", \"b\"]\n```"}}
	got := expandQuery(context.Background(), chat, "model", "q")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestExpandQueryParsesWrappedObject(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: `{"subqueries": ["a", "b"]}`}}
	got := expandQuery(context.Background(), chat, "model", "q")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestExpandQueryCapsAtFive(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: `["a","b","c","d","e","f","g"]`}}
	got := expandQuery(context.Background(), chat, "model", "q")
	if len(got) != 5 {
		t.Fatalf("got %d subqueries, want 5", len(got))
	}
}

func TestExpandQueryInvalidJSONFailsOpen(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: "not json at all"}}
	got := expandQuery(context.Background(), chat, "model", "q")
	if len(got) != 1 || got[0] != "q" {
		t.Fatalf("got %v, want [q]", got)
	}
}

func TestExpandQueryEmptyArrayFailsOpen(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: `[]`}}
	got := expandQuery(context.Background(), chat, "model", "q")
	if len(got) != 1 || got[0] != "q" {
		t.Fatalf("got %v, want [q]", got)
	}
}

func TestExpandQueryFiltersBlankEntries(t *testing.T) {
	chat := fakeChat{resp: llm.ChatResponse{Content: `["a", "  ", "b"]`}}
	got := expandQuery(context.Background(), chat, "model", "q")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}
