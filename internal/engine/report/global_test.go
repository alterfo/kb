package report

import (
	"context"
	"errors"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
)

func TestGlobalReportNoCommunitiesFailsOpen(t *testing.T) {
	got := GlobalReport(context.Background(), fakeChat{resp: llm.ChatResponse{Content: "unused"}}, "m", "q", nil)
	if got != "no community summaries found" {
		t.Fatalf("got %q", got)
	}
}

func TestGlobalReportSkipsCommunitiesWithoutSummary(t *testing.T) {
	communities := []graphstore.Community{{ID: "c1", Title: "T1"}}
	got := GlobalReport(context.Background(), fakeChat{resp: llm.ChatResponse{Content: "unused"}}, "m", "q", communities)
	if got != "no community summaries found" {
		t.Fatalf("got %q", got)
	}
}

func TestGlobalReportNilChatFailsOpenToTitleList(t *testing.T) {
	communities := []graphstore.Community{
		{ID: "c1", Title: "T1", Summary: "s1"},
		{ID: "c2", Title: "T2", Summary: "s2"},
	}
	got := GlobalReport(context.Background(), nil, "m", "q", communities)
	want := "relevant communities found but report synthesis unavailable: T1, T2"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGlobalReportChatErrorFailsOpen(t *testing.T) {
	communities := []graphstore.Community{{ID: "c1", Title: "T1", Summary: "s1"}}
	got := GlobalReport(context.Background(), fakeChat{err: errors.New("boom")}, "m", "q", communities)
	if got != "relevant communities found but report synthesis unavailable: T1" {
		t.Fatalf("got %q", got)
	}
}

func TestGlobalReportUsesChatResponse(t *testing.T) {
	communities := []graphstore.Community{{ID: "c1", Title: "T1", Summary: "s1"}}
	got := GlobalReport(context.Background(), fakeChat{resp: llm.ChatResponse{Content: "the global report"}}, "m", "q", communities)
	if got != "the global report" {
		t.Fatalf("got %q", got)
	}
}
