package retriever

import (
	"context"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
)

// firstCallChat returns the scripted response for the first Chat call only
// (the SetRetrieve expansion pass); every later call (per-round localLegs
// expansion) returns garbage so it fails open to the round query itself.
type firstCallChat struct {
	resp string
	n    int
}

func (f *firstCallChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.n++
	if f.n == 1 {
		return llm.ChatResponse{Content: f.resp}, nil
	}
	return llm.ChatResponse{Content: "not json"}, nil
}

func setTestCorpus() ([]vector.Chunk, func(text string) []float32) {
	chunks := []vector.Chunk{
		{ID: "a1", RefDocID: "doc-a1", Text: "alpha incident report", FileName: "a1.md", FilePath: "notes/a1.md", Source: "slack", Embedding: []float32{1, 0}},
		{ID: "a2", RefDocID: "doc-a2", Text: "alpha follow-up thread", FileName: "a2.md", FilePath: "notes/a2.md", Source: "slack", Embedding: []float32{1, 0}},
		{ID: "b1", RefDocID: "doc-b1", Text: "beta incident postmortem", FileName: "b1.md", FilePath: "jira/b1.md", Source: "jira", Embedding: []float32{0, 1}},
		{ID: "b2", RefDocID: "doc-b2", Text: "beta customer escalation", FileName: "b2.md", FilePath: "jira/b2.md", Source: "jira", Embedding: []float32{0, 1}},
	}
	vec := func(text string) []float32 {
		if strings.Contains(text, "alpha") {
			return []float32{1, 0}
		}
		if strings.Contains(text, "beta") {
			return []float32{0, 1}
		}
		return []float32{1, 1}
	}
	return chunks, vec
}

func newSetRetriever(t *testing.T, expandResponse string, cfgOverrides func(*Config)) *Retriever {
	t.Helper()
	chunks, vec := setTestCorpus()
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)
	cfg := Config{
		Vector:       vs,
		BM25:         idx,
		Hybrid:       true,
		Embed:        fakeEmbedder{vec: vec},
		Chat:         &firstCallChat{resp: expandResponse},
		SetMaxRounds: 5,
	}
	if cfgOverrides != nil {
		cfgOverrides(&cfg)
	}
	return New(cfg)
}

func TestSetRetrieveUnionsDocsAcrossVariants(t *testing.T) {
	r := newSetRetriever(t, `["beta incidents","more alpha work"]`, nil)

	res, evidence, err := r.SetRetrieve(context.Background(), "alpha incidents", vector.Filter{})
	if err != nil {
		t.Fatalf("SetRetrieve: %v", err)
	}

	if res.Count != 4 {
		t.Fatalf("Count = %d (%v), want 4", res.Count, res.DocIDs)
	}
	if len(res.DocIDs) != 4 {
		t.Fatalf("DocIDs = %v, want 4 entries", res.DocIDs)
	}
	if !res.Saturated {
		t.Error("Saturated = false, want true (last variant added nothing)")
	}
	if len(evidence) == 0 {
		t.Fatal("evidence empty")
	}
}

func TestSetRetrieveStopsAtMaxRounds(t *testing.T) {
	r := newSetRetriever(t, `["beta incidents","more alpha work"]`, func(c *Config) {
		c.SetMaxRounds = 2
	})

	res, _, err := r.SetRetrieve(context.Background(), "alpha incidents", vector.Filter{})
	if err != nil {
		t.Fatalf("SetRetrieve: %v", err)
	}
	if res.Count != 4 {
		t.Fatalf("Count = %d, want 4 (both rounds ran)", res.Count)
	}
	if res.Saturated {
		t.Error("Saturated = true, want false when stopped by round budget")
	}
}

func TestSetRetrieveFilterApplies(t *testing.T) {
	r := newSetRetriever(t, `["beta follow-ups","unrelated stuff"]`, nil)

	res, _, err := r.SetRetrieve(context.Background(), "beta incidents", vector.Filter{Sources: []string{"jira"}})
	if err != nil {
		t.Fatalf("SetRetrieve: %v", err)
	}
	if res.Count != 2 {
		t.Fatalf("Count = %d (%v), want 2 jira docs", res.Count, res.DocIDs)
	}
	for _, id := range res.DocIDs {
		if !strings.HasPrefix(id, "doc-b") {
			t.Errorf("DocIDs contains non-jira doc %q", id)
		}
	}
	if !res.Saturated {
		t.Error("Saturated = false, want true (variants converged)")
	}
}

func TestModeSetDispatchPrependsSummary(t *testing.T) {
	r := newSetRetriever(t, `["beta incidents"]`, nil)

	got, err := r.Retrieve(context.Background(), "alpha incidents", Options{K: 10, Mode: ModeSet})
	if err != nil {
		t.Fatalf("Retrieve(ModeSet): %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no results")
	}
	first := got[0]
	if first.Chunk.ID != "set-summary" {
		t.Fatalf("first chunk = %q, want set-summary", first.Chunk.ID)
	}
	if !strings.Contains(first.Chunk.Text, "matched 4 documents") {
		t.Errorf("summary text = %q, want count mention", first.Chunk.Text)
	}
	for _, sc := range got[1:] {
		if sc.Chunk.ID == "set-summary" {
			t.Error("duplicate summary chunk")
		}
	}
}

func TestModeSetRespectsK(t *testing.T) {
	r := newSetRetriever(t, `["beta incidents"]`, nil)
	got, err := r.Retrieve(context.Background(), "alpha incidents", Options{K: 3, Mode: ModeSet})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want k=3 (summary + 2 evidence)", len(got))
	}
}

func TestModeSetNilChatStillScansOriginalQuery(t *testing.T) {
	chunks, vec := setTestCorpus()
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)
	r := New(Config{Vector: vs, BM25: idx, Hybrid: true, Embed: fakeEmbedder{vec: vec}})

	res, _, err := r.SetRetrieve(context.Background(), "alpha incidents", vector.Filter{})
	if err != nil {
		t.Fatalf("SetRetrieve: %v", err)
	}
	if res.Count != 2 {
		t.Fatalf("Count = %d, want 2 alpha docs from original query only", res.Count)
	}
	if !res.Saturated {
		t.Error("Saturated = false, want true without variants")
	}
}

func TestIsSyntheticGraphChunk(t *testing.T) {
	real := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "doc-a1#0", RefDocID: "doc-a1", Source: "slack"}},
		{Chunk: vector.Chunk{ID: "set-summary.md#0", RefDocID: "set-summary", Source: "notes"}},
		{Chunk: vector.Chunk{ID: "graph-doc#0", RefDocID: "graph-doc", Source: "graph"}},
	}
	for _, sc := range real {
		if isSyntheticGraphChunk(sc) {
			t.Errorf("isSyntheticGraphChunk(%q) = true, want false (real document)", sc.Chunk.RefDocID)
		}
	}

	synthetic := []vector.ScoredChunk{
		{Chunk: vector.Chunk{ID: "set-summary", RefDocID: "set-summary", Source: "scan"}},
		{Chunk: vector.Chunk{ID: "community:abc", RefDocID: "community:abc", Source: "graph"}},
		{Chunk: vector.Chunk{ID: "global:answer", RefDocID: "global:answer", Source: "graph"}},
	}
	for _, sc := range synthetic {
		if !isSyntheticGraphChunk(sc) {
			t.Errorf("isSyntheticGraphChunk(%q) = false, want true (synthetic graph chunk)", sc.Chunk.RefDocID)
		}
	}
}

func TestSetRetrieveExcludesSyntheticGraphChunksFromCount(t *testing.T) {
	chunks, vec := setTestCorpus()
	chunks = append(chunks, vector.Chunk{
		ID:        "community:abc",
		RefDocID:  "community:abc",
		Text:      "synthetic community summary",
		FileName:  "abc",
		Source:    "graph",
		Embedding: []float32{1, 1},
	})
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)
	r := New(Config{Vector: vs, BM25: idx, Hybrid: true, Embed: fakeEmbedder{vec: vec}, SetMaxRounds: 5})

	res, _, err := r.SetRetrieve(context.Background(), "incident", vector.Filter{})
	if err != nil {
		t.Fatalf("SetRetrieve: %v", err)
	}
	if res.Count != 4 {
		t.Fatalf("Count = %d (%v), want 4 real documents (synthetic community chunk excluded)", res.Count, res.DocIDs)
	}
	for _, id := range res.DocIDs {
		if strings.HasPrefix(id, "community:") || id == "set-summary" {
			t.Errorf("DocIDs contains synthetic chunk %q", id)
		}
	}
}
