package retriever

import (
	"context"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestRetrieveQualifierFilterFallsBackUnfiltered(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Source: "slack", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	r := New(Config{
		Vector: vs,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: false,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{
		K:                  10,
		Filter:             vector.Filter{Sources: []string{"jira"}},
		relaxFilterOnEmpty: true,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if vs.queryCalls != 2 {
		t.Fatalf("query calls = %d, want 2 (filtered attempt + unfiltered fallback)", vs.queryCalls)
	}
	found := false
	for _, sc := range got {
		if sc.Chunk.ID == "a" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fallback result = %+v, want chunk a", got)
	}
}

func TestRetrieveEmptyFilterDoesNotFallback(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	r := New(Config{
		Vector: vs,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: false,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{K: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if vs.queryCalls != 1 {
		t.Fatalf("query calls = %d, want 1", vs.queryCalls)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("result = %+v, want chunk a", got)
	}
}

func TestRetrieveQualifierFilterEmptyCorpusDoesNotRetry(t *testing.T) {
	vs := &fakeVectorStore{}
	r := New(Config{
		Vector: vs,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: false,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{
		K:                  10,
		Filter:             vector.Filter{Sources: []string{"jira"}},
		relaxFilterOnEmpty: true,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if vs.queryCalls != 1 {
		t.Fatalf("query calls = %d, want 1 (no spurious unfiltered retry)", vs.queryCalls)
	}
	if len(got) != 0 {
		t.Fatalf("result = %+v, want empty", got)
	}
}

func TestRetrieveExplicitFilterDoesNotFallback(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Source: "slack", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	r := New(Config{
		Vector: vs,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: false,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{
		K:      10,
		Filter: vector.Filter{Sources: []string{"jira"}},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if vs.queryCalls != 1 {
		t.Fatalf("query calls = %d, want 1 (explicit filters must not fall back)", vs.queryCalls)
	}
	if len(got) != 0 {
		t.Fatalf("result = %+v, want empty", got)
	}
}

func TestRetrieveQualifierFilterFallsBackWhenGraphCommunityOnly(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Source: "slack", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)

	entity := graphstore.Entity{ID: "apple|concept", Name: "apple"}
	gs := &fakeGraphStore{
		entities: map[string]graphstore.Entity{"apple": entity},
		communities: []graphstore.Community{
			{ID: "c1", Title: "Apple", Summary: "apple community"},
		},
	}
	r := New(Config{
		Vector: vs,
		BM25:   idx,
		Embed:  fakeEmbedder{vec: constVec([]float32{1, 0})},
		Hybrid: true,
		Graph:  gs,
	})

	got, err := r.Retrieve(context.Background(), "apple", Options{
		K:                  10,
		Filter:             vector.Filter{Sources: []string{"jira"}},
		relaxFilterOnEmpty: true,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	found := false
	for _, sc := range got {
		if sc.Chunk.ID == "a" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fallback result = %+v, want chunk a", got)
	}
}

func TestRetrieveDriftQualifierFilterFallsBackUnfiltered(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Source: "slack", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	idx := bm25.New()
	idx.Rebuild(chunks, 1)
	gs := &fakeGraphStore{
		communities: []graphstore.Community{
			{ID: "apple-comm", Level: 0, Title: "apple cluster", Summary: "apple community", SourceChunks: []string{"a"}},
		},
	}
	embedder := fakeEmbedder{vec: func(text string) []float32 {
		if strings.Contains(text, "apple") {
			return []float32{1, 0}
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

	got, err := r.Retrieve(context.Background(), "apple", Options{
		K:               10,
		Mode:            ModeDrift,
		qualifierFilter: vector.Filter{Sources: []string{"jira"}},
	})
	if err != nil {
		t.Fatalf("Retrieve(ModeDrift): %v", err)
	}
	found := false
	for _, sc := range got {
		if sc.Chunk.ID == "a" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("drift fallback result = %+v, want chunk a", got)
	}
}

func TestRetrieveSetQualifierFilterFallsBackUnfiltered(t *testing.T) {
	r := newSetRetriever(t, `["beta incidents"]`, nil)
	got, err := r.Retrieve(context.Background(), "alpha incidents", Options{
		K:               10,
		Mode:            ModeSet,
		qualifierFilter: vector.Filter{Sources: []string{"jira"}},
	})
	if err != nil {
		t.Fatalf("Retrieve(ModeSet): %v", err)
	}
	if len(got) == 0 || got[0].Chunk.ID != "set-summary" {
		t.Fatalf("got = %+v, want set-summary first", got)
	}
	if !strings.Contains(got[0].Chunk.Text, "matched 2 documents") {
		t.Fatalf("summary text = %q, want unfiltered count of 2", got[0].Chunk.Text)
	}
}

func TestAdapterExplicitFilterDoesNotFallback(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Source: "slack", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	r := New(Config{Vector: vs, Embed: fakeEmbedder{vec: constVec([]float32{1, 0})}, Hybrid: false})
	a := Adapter{r}

	got, err := a.RetrieveModeFiltered(context.Background(), "apple", 10, ModeLocal, vector.Filter{Sources: []string{"jira"}}, vector.Filter{})
	if err != nil {
		t.Fatalf("RetrieveModeFiltered: %v", err)
	}
	if vs.queryCalls != 1 {
		t.Fatalf("query calls = %d, want 1 (explicit base filters must not fall back)", vs.queryCalls)
	}
	if len(got) != 0 {
		t.Fatalf("result = %+v, want empty for strict base filter", got)
	}
}

func TestAdapterQualifierFilterFallsBack(t *testing.T) {
	chunks := []vector.Chunk{
		{ID: "a", RefDocID: "doc-a", Text: "apple orchard", FilePath: "notes/a.md", Source: "slack", Embedding: []float32{1, 0}},
	}
	vs := &fakeVectorStore{chunks: chunks}
	r := New(Config{Vector: vs, Embed: fakeEmbedder{vec: constVec([]float32{1, 0})}, Hybrid: false})
	a := Adapter{r}

	got, err := a.RetrieveModeFiltered(context.Background(), "apple", 10, ModeLocal, vector.Filter{}, vector.Filter{Sources: []string{"jira"}})
	if err != nil {
		t.Fatalf("RetrieveModeFiltered: %v", err)
	}
	if len(got) != 1 || got[0].Chunk.ID != "a" {
		t.Fatalf("result = %+v, want fallback chunk a", got)
	}
}
