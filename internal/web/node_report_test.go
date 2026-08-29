package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 0},
		{"   ", 0},
		{"abcd", 1},
		{"12345", 2},
		{"abcdefgh", 2},
	}
	for _, c := range cases {
		if got := estimateTokens(c.text); got != c.want {
			t.Fatalf("estimateTokens(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestAuthorityRank(t *testing.T) {
	cases := []struct {
		authority string
		want      int
	}{
		{"approved", 0},
		{"Approved", 0},
		{"notes", 1},
		{"chat", 2},
		{"", 2},
	}
	for _, c := range cases {
		if got := authorityRank(c.authority); got != c.want {
			t.Fatalf("authorityRank(%q) = %d, want %d", c.authority, got, c.want)
		}
	}
}

func seedNodeReportFixture(t *testing.T, te *testEnv) {
	t.Helper()
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Ship auth", Type: "task", SourceChunks: []string{"task-chunk"}},
		{ID: "code-a", Name: "Code A", Type: "code", SourceChunks: []string{"code-a-chunk"}},
		{ID: "code-b", Name: "Code B", Type: "code", SourceChunks: []string{"code-b-chunk"}},
		{ID: "doc", Name: "Auth doc", Type: "doc", SourceChunks: []string{"doc-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r-a", Src: "task", Dst: "code-a", Type: "code", Confidence: 0.9, Provenance: "extraction", SourceChunks: []string{"task-chunk"}},
		{ID: "r-b", Src: "task", Dst: "code-b", Type: "code", Confidence: 0.9, Provenance: "extraction", SourceChunks: []string{"task-chunk"}},
		{ID: "r-doc", Src: "task", Dst: "doc", Type: "documents", Confidence: 1.0, Provenance: "go-code", SourceChunks: []string{"task-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	if err := te.graph.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "comm", Title: "Auth cluster", Members: []string{"task", "code-a", "code-b"}, Summary: "cluster summary"},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}
	if err := te.vector.Upsert(ctx, []vector.Chunk{
		{ID: "task-chunk", FileName: "task.md", Text: "task body"},
		{ID: "code-a-chunk", FileName: "a.go", Text: "code a text"},
		{ID: "code-b-chunk", FileName: "b.go", Text: "code b text"},
		{ID: "doc-chunk", FileName: "doc.md", Text: "doc text"},
	}); err != nil {
		t.Fatalf("vector Upsert: %v", err)
	}
}

func TestNodeReportContext_SelectsTopKPerTypeAndDropsRest(t *testing.T) {
	te := newTestEnv(t, nil)
	seedNodeReportFixture(t, te)

	ctx := context.Background()
	data := te.server.buildNodeReportContext(ctx, "task", 1)
	if data.Entity.ID != "task" {
		t.Fatalf("entity = %+v", data.Entity)
	}
	if len(data.Chunks) != 4 {
		t.Fatalf("chunks = %d, want 4 (node + one code + one doc + community): %+v", len(data.Chunks), data.Chunks)
	}
	if data.TokenEstimate == 0 {
		t.Fatalf("token estimate = 0")
	}
	if len(data.Dropped) != 1 || data.Dropped[0].Name != "Code B" || !strings.Contains(data.Dropped[0].Reason, "лимит 1") {
		t.Fatalf("dropped = %+v, want Code B dropped by type limit", data.Dropped)
	}

	groupCounts := map[string]int{}
	for _, group := range data.Groups {
		groupCounts[group.Name] = group.Count
	}
	if groupCounts["code neighbours"] != 1 || groupCounts["documents neighbours"] != 1 || groupCounts["community summaries"] != 1 {
		t.Fatalf("groups = %+v", data.Groups)
	}
}

func TestNodeReportContext_DiskDocDedupsSharedNodeChunks(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(te.root, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	docBody := "task body from disk"
	if err := os.WriteFile(filepath.Join(te.root, "notes", "task.md"), []byte(docBody), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Ship auth", Type: "task", SourceChunks: []string{"task-chunk"}},
		{ID: "code-a", Name: "Code A", Type: "code", SourceChunks: []string{"task-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r-a", Src: "task", Dst: "code-a", Type: "code", Confidence: 0.9, SourceChunks: []string{"task-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	if err := te.vector.Upsert(ctx, []vector.Chunk{{
		ID: "task-chunk", FileName: "task.md", FilePath: "notes/task.md", Text: docBody,
	}}); err != nil {
		t.Fatalf("vector Upsert: %v", err)
	}

	data := te.server.buildNodeReportContext(ctx, "task", 10)
	if len(data.Chunks) != 1 {
		t.Fatalf("chunks = %d, want exactly 1 (disk doc only, shared chunk deduped): %+v", len(data.Chunks), data.Chunks)
	}
	if !strings.Contains(data.Chunks[0].Text, docBody) {
		t.Fatalf("chunk text = %q, want disk doc", data.Chunks[0].Text)
	}
	text := ""
	for _, chunk := range data.Chunks {
		text += chunk.Text
	}
	if strings.Count(text, docBody) != 1 {
		t.Fatalf("doc body appears %d times in context, want once", strings.Count(text, docBody))
	}
}

func TestNodeReportContext_DiskDocKeepsChunksFromOtherDocuments(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(te.root, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(te.root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	docBody := "task body from disk"
	otherBody := "other document text"
	if err := os.WriteFile(filepath.Join(te.root, "notes", "task.md"), []byte(docBody), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(te.root, "docs", "other.md"), []byte(otherBody), 0o644); err != nil {
		t.Fatalf("write other: %v", err)
	}
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Ship auth", Type: "task", SourceChunks: []string{"c-a", "c-b"}},
		{ID: "code-a", Name: "Code A", Type: "code", SourceChunks: []string{"c-b"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r-a", Src: "task", Dst: "code-a", Type: "code", Confidence: 0.9, SourceChunks: []string{"c-b"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	if err := te.vector.Upsert(ctx, []vector.Chunk{
		{ID: "c-a", FileName: "task.md", FilePath: "notes/task.md", Text: docBody},
		{ID: "c-b", FileName: "other.md", FilePath: "docs/other.md", Text: otherBody},
	}); err != nil {
		t.Fatalf("vector Upsert: %v", err)
	}

	data := te.server.buildNodeReportContext(ctx, "task", 10)
	if len(data.Chunks) != 2 {
		t.Fatalf("chunks = %d, want 2 (disk doc + other-doc chunk kept): %+v", len(data.Chunks), data.Chunks)
	}
	text := ""
	for _, chunk := range data.Chunks {
		text += chunk.Text
	}
	if !strings.Contains(text, otherBody) {
		t.Fatalf("other-document chunk lost from context: %q", text)
	}
}

func TestNodeReport_EstimateDialogShowsBudget(t *testing.T) {
	te := newTestEnv(t, nil)
	seedNodeReportFixture(t, te)

	rr := getPage(t, te.server.Handler(), "/reports/node/estimate?id=task")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Оценка контекста") || !strings.Contains(body, "токенов") {
		t.Fatalf("estimate dialog missing token budget: %s", body)
	}
	if !strings.Contains(body, "code neighbours") || !strings.Contains(body, "community summaries") {
		t.Fatalf("estimate dialog missing context breakdown: %s", body)
	}
}

func TestNodeReport_SynthesisFallbackShowsWarningAndContextSection(t *testing.T) {
	te := newTestEnv(t, nil)
	seedNodeReportFixture(t, te)

	rr := postForm(t, te.server.Handler(), "/reports", url.Values{"node": {"task"}, "q": {"report"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "синтез недоступен") {
		t.Fatalf("fallback warning missing: %s", body)
	}
	if !strings.Contains(body, "Что вошло в контекст") {
		t.Fatalf("context section missing: %s", body)
	}
}

func TestNodeReport_UsesNodeScopedContextInPrompt(t *testing.T) {
	var prompt string
	chat := &fakeChat{fn: func(req llm.ChatRequest) (llm.ChatResponse, error) {
		prompt = req.Messages[len(req.Messages)-1].Content
		return llm.ChatResponse{Content: "node report answer"}, nil
	}}
	te := newTestEnv(t, chat)
	seedNodeReportFixture(t, te)

	if err := te.vector.Upsert(context.Background(), []vector.Chunk{
		{ID: "unrelated-chunk", FileName: "unrelated.md", Text: "unrelated secret content"},
	}); err != nil {
		t.Fatalf("vector Upsert unrelated: %v", err)
	}

	rr := postForm(t, te.server.Handler(), "/reports", url.Values{"node": {"task"}, "q": {"summarize"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "node report answer") {
		t.Fatalf("synthesis answer missing: %s", rr.Body.String())
	}
	for _, want := range []string{"task body", "code a text", "code b text", "doc text", "cluster summary"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "unrelated secret content") {
		t.Fatalf("prompt leaked unrelated entity text: %s", prompt)
	}
}
