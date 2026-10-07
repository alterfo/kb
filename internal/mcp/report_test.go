package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/store/graphstore"
)

func TestGenerateReport_SearchModeFailsOpenToSourceListing(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	writeDoc(t, te.root, "notes/doc1.md", connector.Document{ID: "doc1", Source: "notes", Body: "unique-report-token here"})
	if err := te.indexer.AddOrUpdateDocument(ctx, "notes/doc1.md"); err != nil {
		t.Fatalf("AddOrUpdateDocument: %v", err)
	}

	_, out, err := te.server.generateReport(ctx, nil, generateReportIn{Mode: "search", Query: "unique-report-token"})
	if err != nil {
		t.Fatalf("generateReport: %v", err)
	}
	if !strings.Contains(out.Report, "doc1.md") {
		t.Fatalf("generateReport: report = %q, want it to mention doc1.md", out.Report)
	}
}

func TestGenerateReport_GlobalModeUsesCommunities(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "comm:1", Level: 0, Members: []string{"e:alice"}, Title: "Team Alpha", Summary: "Alice's team works on X.", SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	_, out, err := te.server.generateReport(ctx, nil, generateReportIn{Mode: "global", Query: "what teams exist?"})
	if err != nil {
		t.Fatalf("generateReport: %v", err)
	}
	if !strings.Contains(out.Report, "Team Alpha") {
		t.Fatalf("generateReport: report = %q, want it to mention Team Alpha", out.Report)
	}
}

func TestGenerateReport_DiagramModeRendersMermaid(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e:web", Name: "Web", Type: "component", SourceChunks: []string{"c1"}},
		{ID: "e:db", Name: "Database", Type: "storage", SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r:1", Src: "e:web", Dst: "e:db", Type: "reads", SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	_, out, err := te.server.generateReport(ctx, nil, generateReportIn{Mode: "diagram", Query: "Web"})
	if err != nil {
		t.Fatalf("generateReport: %v", err)
	}
	for _, want := range []string{"```mermaid", "flowchart LR", "Web", "Database", "reads"} {
		if !strings.Contains(out.Report, want) {
			t.Fatalf("report missing %q:\n%s", want, out.Report)
		}
	}
}

func TestGenerateReport_DiagramModeUnknownEntityFailsOpen(t *testing.T) {
	te := newTestEnv(t, nil)
	_, out, err := te.server.generateReport(context.Background(), nil, generateReportIn{Mode: "diagram", Query: "nothing-here"})
	if err != nil {
		t.Fatalf("generateReport: %v", err)
	}
	if !strings.Contains(out.Report, "no entity matches") {
		t.Fatalf("report = %q", out.Report)
	}
}

func TestGenerateReport_UnknownModeRejected(t *testing.T) {
	te := newTestEnv(t, nil)
	if _, _, err := te.server.generateReport(context.Background(), nil, generateReportIn{Mode: "bogus", Query: "x"}); err == nil {
		t.Fatalf("generateReport: got nil error for unknown mode, want rejection")
	}
}

func TestGenerateReport_GlobalModeNilGraphFailsOpen(t *testing.T) {
	te := newTestEnv(t, nil)
	te.server.deps.Graph = nil
	_, out, err := te.server.generateReport(context.Background(), nil, generateReportIn{Mode: "global", Query: "x"})
	if err != nil {
		t.Fatalf("generateReport: %v", err)
	}
	if out.Report == "" {
		t.Fatalf("generateReport: got empty report, want fail-open placeholder")
	}
}
