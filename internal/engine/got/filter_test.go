package got

import (
	"context"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

type filterCapturingRetriever struct {
	fakeRetriever
	baseFilters      []vector.Filter
	qualifierFilters []vector.Filter
}

func (f *filterCapturingRetriever) RetrieveModeFiltered(ctx context.Context, query string, k int, mode retriever.Mode, base, qualifier vector.Filter) ([]vector.ScoredChunk, error) {
	f.baseFilters = append(f.baseFilters, base)
	f.qualifierFilters = append(f.qualifierFilters, qualifier)
	return f.fakeRetriever.RetrieveMode(ctx, query, k, mode)
}

var _ interface {
	RetrieveMode(ctx context.Context, query string, k int, mode retriever.Mode) ([]vector.ScoredChunk, error)
} = (*filterCapturingRetriever)(nil)

type countingChat struct {
	inner          scriptedChat
	calls          int
	qualifierCalls int
}

func (c *countingChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	c.calls++
	for _, m := range req.Messages {
		if m.Role == "system" && strings.HasPrefix(m.Content, "You extract structured metadata qualifiers") {
			c.qualifierCalls++
			break
		}
	}
	return c.inner.Chat(ctx, req)
}

const qualifierResponse = `{"sources":["slack"],"metadata":{"region":"us-east"}}`

func qualifierScripted() scriptedChat {
	return scriptedChat{
		byPrompt: map[string]llm.ChatResponse{
			"You extract structured metadata qualifiers": {Content: qualifierResponse},
		},
	}
}

func TestOrchestratorPassesConfigFilter(t *testing.T) {
	calls := 0
	cap := &filterCapturingRetriever{fakeRetriever: fakeRetriever{calls: &calls}}
	want := vector.Filter{Sources: []string{"jira"}, Metadata: map[string]string{"region": "us-east"}}

	o := New(Config{Retriever: cap, Filter: want, Sleep: instantSleep})
	o.Run(context.Background(), "what changed")

	if len(cap.baseFilters) == 0 {
		t.Fatal("retriever was never invoked through the filtered path")
	}
	for i, f := range cap.baseFilters {
		if len(f.Sources) != 1 || f.Sources[0] != "jira" {
			t.Fatalf("call %d base filter = %+v, want config filter passed through", i, f)
		}
		if len(cap.qualifierFilters[i].Sources) != 0 {
			t.Fatalf("call %d qualifier filter = %+v, want empty qualifier", i, cap.qualifierFilters[i])
		}
	}
}

func TestOrchestratorExtractsQualifiersOnce(t *testing.T) {
	calls := 0
	cap := &filterCapturingRetriever{fakeRetriever: fakeRetriever{calls: &calls}}
	chat := &countingChat{inner: qualifierScripted()}

	o := New(Config{
		Retriever:         cap,
		Chat:              chat,
		ExtractQualifiers: true,
		Sleep:             instantSleep,
	})
	o.Run(context.Background(), "open slack incidents in us-east")

	if len(cap.baseFilters) == 0 {
		t.Fatal("retriever never invoked")
	}
	firstBase := cap.baseFilters[0]
	if len(firstBase.Sources) != 0 {
		t.Fatalf("base filter sources = %v, want empty base", firstBase.Sources)
	}
	firstQualifier := cap.qualifierFilters[0]
	if len(firstQualifier.Sources) != 1 || firstQualifier.Sources[0] != "slack" {
		t.Fatalf("qualifier filter sources = %v, want extracted slack", firstQualifier.Sources)
	}
	if firstQualifier.Metadata["region"] != "us-east" {
		t.Fatalf("qualifier filter metadata = %v, want extracted region", firstQualifier.Metadata)
	}
	if chat.qualifierCalls != 1 {
		t.Fatalf("qualifier extraction ran %d times, want exactly 1 (total chat=%d, retrieval=%d)", chat.qualifierCalls, chat.calls, len(cap.baseFilters))
	}
}

func TestOrchestratorExtractionFailOpenKeepsConfigFilter(t *testing.T) {
	calls := 0
	cap := &filterCapturingRetriever{fakeRetriever: fakeRetriever{calls: &calls}}
	chat := scriptedChat{}

	o := New(Config{
		Retriever:         cap,
		Chat:              chat,
		ExtractQualifiers: true,
		Sleep:             instantSleep,
		Filter:            vector.Filter{Sources: []string{"gmail"}},
	})
	o.Run(context.Background(), "anything")

	if len(cap.baseFilters) == 0 {
		t.Fatal("retriever never invoked")
	}
	for _, f := range cap.baseFilters {
		if len(f.Sources) != 1 || f.Sources[0] != "gmail" {
			t.Fatalf("base filter = %+v, want config filter preserved on extraction failure", f)
		}
	}
	for _, f := range cap.qualifierFilters {
		if len(f.Sources) != 0 {
			t.Fatalf("qualifier filter = %+v, want empty on extraction failure", f)
		}
	}
}
