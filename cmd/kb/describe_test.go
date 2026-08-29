package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/render"
	"github.com/alterfo/kb/internal/sink"
)

type describeFakeChat struct {
	fn func(req llm.ChatRequest) (llm.ChatResponse, error)
}

func (f describeFakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if f.fn == nil {
		return llm.ChatResponse{}, errors.New("no fake chat response")
	}
	return f.fn(req)
}

func writeRenderFile(t *testing.T, root, rel string, doc connector.Document) {
	t.Helper()
	raw, err := render.Render(doc)
	if err != nil {
		t.Fatalf("render.Render: %v", err)
	}
	if err := sink.WritePath(root, rel, raw); err != nil {
		t.Fatalf("sink.WritePath: %v", err)
	}
}

func readParsedFile(t *testing.T, root, rel string) connector.Document {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	doc, err := render.Parse(data)
	if err != nil {
		t.Fatalf("render.Parse: %v", err)
	}
	return doc
}

func TestRunDescribe_SuccessSkipsExistingSummary(t *testing.T) {
	root := t.TempDir()
	writeRenderFile(t, root, "notes/new.md", connector.Document{ID: "new", Source: "notes", Title: "New", Body: "First body."})
	writeRenderFile(t, root, "notes/done.md", connector.Document{ID: "done", Source: "notes", Title: "Done", Body: "Second body.", Summary: "Already set."})

	var indexed []string
	chat := describeFakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{Content: `["Generated summary."]`}, nil
	}}

	res, err := runDescribe(context.Background(), describeDeps{
		Root:  root,
		Model: "test-model",
		Batch: 2,
		Chat:  chat,
		Write: func(rel string, data []byte) error { return sink.WritePath(root, rel, data) },
		Index: func(ctx context.Context, rel string) error {
			indexed = append(indexed, rel)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runDescribe: %v", err)
	}
	if res.Written != 1 || res.Generated != 1 || res.Failed != 0 || res.Skipped != 1 {
		t.Fatalf("result = %+v, want written=1 generated=1 failed=0", res)
	}
	if got := readParsedFile(t, root, "notes/new.md").Summary; got != "Generated summary." {
		t.Fatalf("new summary = %q, want Generated summary.", got)
	}
	if got := readParsedFile(t, root, "notes/done.md").Summary; got != "Already set." {
		t.Fatalf("existing summary changed to %q", got)
	}
	if len(indexed) != 1 || indexed[0] != "notes/new.md" {
		t.Fatalf("indexed = %v, want [notes/new.md]", indexed)
	}
}

func TestRunDescribe_LLMErrorFallsBack(t *testing.T) {
	root := t.TempDir()
	writeRenderFile(t, root, "notes/fallback.md", connector.Document{ID: "f", Source: "notes", Title: "F", Body: "First sentence. Second sentence."})

	res, err := runDescribe(context.Background(), describeDeps{
		Root:  root,
		Model: "test-model",
		Batch: 2,
		Chat:  describeFakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) { return llm.ChatResponse{}, errors.New("boom") }},
		Write: func(rel string, data []byte) error { return sink.WritePath(root, rel, data) },
	})
	if err != nil {
		t.Fatalf("runDescribe: %v", err)
	}
	if res.Written != 1 || res.Generated != 0 || res.Failed != 0 {
		t.Fatalf("result = %+v, want written=1 generated=0 failed=0", res)
	}
	if got := readParsedFile(t, root, "notes/fallback.md").Summary; got != "First sentence." {
		t.Fatalf("fallback summary = %q, want First sentence.", got)
	}
}

func TestRunDescribe_IndexFailureRevertsWrittenSummary(t *testing.T) {
	root := t.TempDir()
	writeRenderFile(t, root, "notes/new.md", connector.Document{ID: "new", Source: "notes", Title: "New", Body: "First body."})

	chat := describeFakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
		return llm.ChatResponse{Content: `["Generated summary."]`}, nil
	}}
	res, err := runDescribe(context.Background(), describeDeps{
		Root:  root,
		Model: "test-model",
		Batch: 2,
		Chat:  chat,
		Write: func(rel string, data []byte) error { return sink.WritePath(root, rel, data) },
		Index: func(ctx context.Context, rel string) error { return errors.New("index down") },
	})
	if err != nil {
		t.Fatalf("runDescribe: %v", err)
	}
	if res.Failed != 1 || res.Written != 0 {
		t.Fatalf("result = %+v, want failed=1 written=0", res)
	}
	if got := readParsedFile(t, root, "notes/new.md").Summary; got != "" {
		t.Fatalf("summary should be reverted after index failure, got %q", got)
	}
}

func TestCollectDescribeCandidates_SourceFilter(t *testing.T) {
	root := t.TempDir()
	writeRenderFile(t, root, "notes/a.md", connector.Document{ID: "a", Source: "notes", Title: "A", Body: "A"})
	writeRenderFile(t, root, "github/b.md", connector.Document{ID: "b", Source: "github", Title: "B", Body: "B"})
	writeRenderFile(t, root, "notes/done.md", connector.Document{ID: "done", Source: "notes", Title: "Done", Body: "Done", Summary: "Done"})

	got, skipped, collectErrs, err := collectDescribeCandidates(root, "notes")
	if err != nil {
		t.Fatalf("collectDescribeCandidates: %v", err)
	}
	if len(got) != 1 || got[0].rel != "notes/a.md" {
		t.Fatalf("candidates = %+v, want only notes/a.md", got)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if collectErrs != 0 {
		t.Fatalf("collectErrs = %d, want 0", collectErrs)
	}
}

func TestParseSummariesResponse(t *testing.T) {
	got, err := parseSummariesResponse("```json\n[\"one\",\"two\"]\n```")
	if err != nil {
		t.Fatalf("parseSummariesResponse: %v", err)
	}
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("summaries = %v", got)
	}
}

func TestFallbackSummary(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"Short body without punctuation.", "Short body without punctuation."},
		{"First sentence. More text that follows.", "First sentence."},
		{"Question? Answer goes here.", "Question?"},
		{"Exclamation! And then more.", "Exclamation!"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := fallbackSummary(tc.body); got != tc.want {
			t.Errorf("fallbackSummary(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}
