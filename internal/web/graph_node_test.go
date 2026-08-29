package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestGraphNode_ReturnsGroupedEdgesCommunityAndDocument(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Ship auth", Type: "task", Description: "ship the auth work", SourceChunks: []string{"task-chunk"}},
		{ID: "code-login", Name: "Login handler", Type: "code", SourceChunks: []string{"code-login-chunk"}},
		{ID: "code-signup", Name: "Signup handler", Type: "code", SourceChunks: []string{"code-signup-chunk"}},
		{ID: "doc-auth", Name: "Auth doc", Type: "doc", SourceChunks: []string{"doc-auth-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r-login", Src: "task", Dst: "code-login", Type: "code", Confidence: 0.9, Provenance: "extraction", SourceChunks: []string{"task-chunk"}},
		{ID: "r-signup", Src: "task", Dst: "code-signup", Type: "code", Confidence: 0.8, Provenance: "extraction", SourceChunks: []string{"task-chunk"}},
		{ID: "r-doc", Src: "task", Dst: "doc-auth", Type: "documents", Confidence: 1.0, Provenance: "go-code", SourceChunks: []string{"task-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	if err := te.graph.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "comm", Title: "Auth cluster", Members: []string{"task", "code-login", "code-signup"}, Summary: "auth work"},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/graph/node?id=task")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp graphNodeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Document.Name != "Ship auth" || resp.Document.Type != "task" || resp.Document.Description != "ship the auth work" {
		t.Fatalf("document = %+v", resp.Document)
	}
	if len(resp.Document.SourceChunks) != 1 || resp.Document.SourceChunks[0] != "task-chunk" {
		t.Fatalf("document source chunks = %+v", resp.Document.SourceChunks)
	}
	if len(resp.Edges) != 2 {
		t.Fatalf("edge groups = %d, want 2", len(resp.Edges))
	}
	if resp.Edges[0].Type != "code" || resp.Edges[0].Count != 2 || len(resp.Edges[0].Edges) != 2 {
		t.Fatalf("code group = %+v", resp.Edges[0])
	}
	if resp.Edges[0].Edges[0].Confidence == 0 || resp.Edges[0].Edges[0].Provenance != "extraction" {
		t.Fatalf("edge missing confidence/provenance: %+v", resp.Edges[0].Edges[0])
	}
	if resp.Edges[1].Type != "documents" || resp.Edges[1].Count != 1 {
		t.Fatalf("documents group = %+v", resp.Edges[1])
	}
	if len(resp.Community) != 1 || resp.Community[0].Title != "Auth cluster" || resp.Community[0].Summary != "auth work" {
		t.Fatalf("community = %+v", resp.Community)
	}
}

func TestGraphNode_FoldsHubEdgesAndHonorsLimit(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	entities := []graphstore.Entity{{ID: "hub", Name: "Hub", Type: "task", SourceChunks: []string{"hub-chunk"}}}
	relations := make([]graphstore.Relation, 0, 12)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("n-%02d", i)
		entities = append(entities, graphstore.Entity{ID: id, Name: "Neighbor " + id, Type: "code", SourceChunks: []string{"hub-chunk"}})
		relations = append(relations, graphstore.Relation{
			ID: fmt.Sprintf("r-%02d", i), Src: "hub", Dst: id, Type: "code",
			Confidence: 0.9, Provenance: "extraction", SourceChunks: []string{"hub-chunk"},
		})
	}
	if err := te.graph.UpsertEntities(ctx, entities); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, relations); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/graph/node?id=hub")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp graphNodeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Edges) != 1 || resp.Edges[0].Count != 12 || !resp.Edges[0].Foldable {
		t.Fatalf("hub group = %+v", resp.Edges)
	}
	if len(resp.Edges[0].Edges) != defaultGraphNodeLimit {
		t.Fatalf("default folded edges = %d, want %d", len(resp.Edges[0].Edges), defaultGraphNodeLimit)
	}

	rr = getPage(t, te.server.Handler(), "/graph/node?id=hub&limit=3")
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode limit response: %v", err)
	}
	if len(resp.Edges) != 1 || !resp.Edges[0].Foldable || len(resp.Edges[0].Edges) != 3 {
		t.Fatalf("limit=3 group = %+v", resp.Edges)
	}
}

func TestGraphNode_InferredTriggersCapsAndFiltersConnected(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	entities := []graphstore.Entity{
		{ID: "task", Name: "Task Node", Type: "task", Description: "mentions Beta Cand00 Cand01 Cand02 Cand03 Cand04 Cand05 Cand06 Cand07 Cand08 Cand09 Cand10 Cand11", SourceChunks: []string{"task-chunk"}},
		{ID: "beta", Name: "Beta", Type: "doc", SourceChunks: []string{"beta-chunk"}},
	}
	relations := []graphstore.Relation{
		{ID: "task-beta", Src: "task", Dst: "beta", Type: "documents", Confidence: 1.0, Provenance: "go-code", SourceChunks: []string{"task-chunk"}},
	}
	for i := 0; i < 12; i++ {
		entities = append(entities, graphstore.Entity{ID: fmt.Sprintf("cand-%02d", i), Name: fmt.Sprintf("Cand%02d", i), Type: "doc", SourceChunks: []string{"cand-chunk"}})
	}
	if err := te.graph.UpsertEntities(ctx, entities); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, relations); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/graph/node?id=task")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp graphNodeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Inferred) != maxGraphNodeInferred {
		t.Fatalf("inferred = %d, want %d: %+v", len(resp.Inferred), maxGraphNodeInferred, resp.Inferred)
	}
	for _, inferred := range resp.Inferred {
		if inferred.Provenance != provenanceEntityLinking {
			t.Fatalf("inferred provenance = %q, want %q", inferred.Provenance, provenanceEntityLinking)
		}
		if inferred.EntityID == "beta" || inferred.EntityID == "task" {
			t.Fatalf("inferred contains already-connected or self entity: %+v", inferred)
		}
	}
}

func TestGraphNode_MinConfidenceFiltersVerifiedEdges(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Task", Type: "task", SourceChunks: []string{"task-chunk"}},
		{ID: "low", Name: "Low", Type: "code", SourceChunks: []string{"low-chunk"}},
		{ID: "high", Name: "High", Type: "code", SourceChunks: []string{"high-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "low-rel", Src: "task", Dst: "low", Type: "code", Confidence: 0.4, Provenance: "extraction", SourceChunks: []string{"task-chunk"}},
		{ID: "high-rel", Src: "task", Dst: "high", Type: "code", Confidence: 0.9, Provenance: "extraction", SourceChunks: []string{"task-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/graph/node?id=task&min_confidence=0.5")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp graphNodeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Edges) != 1 || resp.Edges[0].Count != 1 || resp.Edges[0].Edges[0].NeighborID != "high" {
		t.Fatalf("min_confidence result = %+v", resp.Edges)
	}
}

func TestGraphNode_EmptyCommunityIsNotAnError(t *testing.T) {
	te := newTestEnv(t, nil)
	if err := te.graph.UpsertEntities(context.Background(), []graphstore.Entity{
		{ID: "task", Name: "Task", Type: "task", SourceChunks: []string{"task-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	rr := getPage(t, te.server.Handler(), "/graph/node?id=task")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp graphNodeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Community) != 0 {
		t.Fatalf("community = %+v, want empty", resp.Community)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", resp.Warnings)
	}
}

type failingNodeGraphStore struct {
	graphstore.Store
}

func (f failingNodeGraphStore) AllEntities(ctx context.Context) ([]graphstore.Entity, error) {
	return nil, errors.New("boom")
}

func TestGraphNode_FailOpenOnStoreError(t *testing.T) {
	srv := NewServer(Deps{Root: t.TempDir(), Graph: failingNodeGraphStore{}})
	rr := getPage(t, srv.Handler(), "/graph/node?id=task")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 fail-open: %s", rr.Code, rr.Body.String())
	}
	var resp graphNodeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Edges) != 0 {
		t.Fatalf("edges = %+v, want empty", resp.Edges)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "loading entity failed") {
		t.Fatalf("warnings = %+v, want loading entity failure", resp.Warnings)
	}
}

func TestBuildGraphNodeEdgeGroups_ConfidenceAndDuplicates(t *testing.T) {
	relations := []graphstore.Relation{
		{ID: "r-low", Src: "task", Dst: "low", Type: "code", Confidence: 0.4},
		{ID: "r-high-a", Src: "task", Dst: "high-a", Type: "code", Confidence: 0.9},
		{ID: "r-high-b", Src: "task", Dst: "high-b", Type: "code", Confidence: 0.9},
	}
	neighbors := map[string]graphstore.Entity{
		"low":    {ID: "low", Name: "Low", Type: "code"},
		"high-a": {ID: "high-a", Name: "High A", Type: "code"},
		"high-b": {ID: "high-b", Name: "High B", Type: "code"},
	}
	groups := buildGraphNodeEdgeGroups("task", relations, neighbors, 0.5, defaultGraphNodeLimit, nil, time.Now())
	if len(groups) != 1 || groups[0].Count != 2 {
		t.Fatalf("groups = %+v, want one group with two confident edges", groups)
	}
	seen := map[string]bool{}
	for _, edge := range groups[0].Edges {
		if seen[edge.NeighborID] {
			t.Fatalf("duplicate neighbor %q", edge.NeighborID)
		}
		seen[edge.NeighborID] = true
	}
}

func TestBuildGraphNodeEdgeGroups_TruncationKeepsFreshestChunkEvidence(t *testing.T) {
	now := time.Now()
	stale := now.Add(-30 * 24 * time.Hour)
	fresh := now.Add(-2 * time.Hour)
	chunks := map[string]vector.Chunk{
		"c-stale": {ID: "c-stale", CreatedAt: stale.UTC().Format(time.RFC3339Nano)},
		"c-fresh": {ID: "c-fresh", CreatedAt: fresh.UTC().Format(time.RFC3339Nano)},
	}
	relations := []graphstore.Relation{
		{ID: "r-new-insert", Src: "task", Dst: "new-evidence", Type: "code", Confidence: 1, SourceChunks: []string{"c-stale"}},
		{ID: "r-old-insert", Src: "task", Dst: "old-evidence", Type: "code", Confidence: 1, SourceChunks: []string{"c-fresh"}},
	}
	neighbors := map[string]graphstore.Entity{
		"old-evidence": {ID: "old-evidence", Name: "Old Evidence", Type: "code"},
		"new-evidence": {ID: "new-evidence", Name: "New Evidence", Type: "code"},
	}
	groups := buildGraphNodeEdgeGroups("task", relations, neighbors, 0, 1, chunks, now)
	if len(groups) != 1 || len(groups[0].Edges) != 1 {
		t.Fatalf("groups = %+v, want one group truncated to one edge", groups)
	}
	if groups[0].Edges[0].NeighborID != "old-evidence" {
		t.Fatalf("kept edge = %+v, want the fresher-chunk evidence (old-evidence)", groups[0].Edges[0])
	}
}
