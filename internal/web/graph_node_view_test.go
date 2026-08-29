package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

func TestGraphNode_DensityLeafAndHub(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()

	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "leaf", Name: "Leaf", Type: "task", SourceChunks: []string{"c"}},
		{ID: "n1", Name: "N1", Type: "code", SourceChunks: []string{"c"}},
		{ID: "n2", Name: "N2", Type: "code", SourceChunks: []string{"c"}},
	}); err != nil {
		t.Fatalf("UpsertEntities leaf: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "l1", Src: "leaf", Dst: "n1", Type: "code", Confidence: 1, SourceChunks: []string{"c"}},
		{ID: "l2", Src: "leaf", Dst: "n2", Type: "code", Confidence: 1, SourceChunks: []string{"c"}},
	}); err != nil {
		t.Fatalf("UpsertRelations leaf: %v", err)
	}
	leaf := getGraphNode(t, te, "leaf")
	if leaf.TotalNeighbors != 2 || leaf.Hub {
		t.Fatalf("leaf density = neighbors %d hub %v, want 2 / false", leaf.TotalNeighbors, leaf.Hub)
	}

	hubEntities := []graphstore.Entity{{ID: "hub", Name: "Hub", Type: "task", SourceChunks: []string{"c"}}}
	hubRelations := make([]graphstore.Relation, 0, 25)
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("hn-%02d", i)
		hubEntities = append(hubEntities, graphstore.Entity{ID: id, Name: "HN " + id, Type: "code", SourceChunks: []string{"c"}})
		hubRelations = append(hubRelations, graphstore.Relation{ID: "hr-" + id, Src: "hub", Dst: id, Type: "code", Confidence: 1, SourceChunks: []string{"c"}})
	}
	if err := te.graph.UpsertEntities(ctx, hubEntities); err != nil {
		t.Fatalf("UpsertEntities hub: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, hubRelations); err != nil {
		t.Fatalf("UpsertRelations hub: %v", err)
	}
	hub := getGraphNode(t, te, "hub")
	if hub.TotalNeighbors != 25 || !hub.Hub {
		t.Fatalf("hub density = neighbors %d hub %v, want 25 / true", hub.TotalNeighbors, hub.Hub)
	}
	if len(hub.Edges) != 1 || !hub.Edges[0].Foldable || hub.Edges[0].Count != 25 {
		t.Fatalf("hub groups = %+v, want one foldable group of 25", hub.Edges)
	}
}

func TestGraphNode_EdgeCarriesAuthorityAndRecency(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Task", Type: "task", SourceChunks: []string{"task-chunk"}},
		{ID: "code", Name: "Code", Type: "code", SourceChunks: []string{"code-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r", Src: "task", Dst: "code", Type: "code", Confidence: 0.9, Provenance: "extraction", SourceChunks: []string{"task-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	if err := te.vector.Upsert(ctx, []vector.Chunk{{
		ID: "task-chunk", RefDocID: "notes/task", Text: "task body", Source: "notes",
		Metadata:  map[string]string{"visibility": "approved"},
		CreatedAt: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano),
	}}); err != nil {
		t.Fatalf("vector Upsert: %v", err)
	}

	resp := getGraphNode(t, te, "task")
	if len(resp.Edges) != 1 || len(resp.Edges[0].Edges) != 1 {
		t.Fatalf("edges = %+v", resp.Edges)
	}
	edge := resp.Edges[0].Edges[0]
	if edge.Authority != "approved" {
		t.Fatalf("authority = %q, want approved", edge.Authority)
	}
	if edge.Recency != "2d" {
		t.Fatalf("recency = %q, want 2d", edge.Recency)
	}
}

func TestGraphNode_AcceptInferredWritesRelationAndRefetchDropsIt(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Task Node", Type: "task", Description: "mentions Cand", SourceChunks: []string{"task-chunk"}},
		{ID: "cand", Name: "Cand", Type: "code", SourceChunks: []string{"cand-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}

	before := getGraphNode(t, te, "task")
	found := false
	for _, inf := range before.Inferred {
		if inf.EntityID == "cand" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("inferred before accept = %+v, want cand", before.Inferred)
	}

	rr := postForm(t, te.server.Handler(), "/graph/relations", url.Values{
		"src": {"task"}, "dst": {"cand"}, "type": {"mentions"},
		"provenance": {"user-accepted"}, "source_chunks": {"task-chunk"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	relations, err := te.graph.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 {
		t.Fatalf("relations after accept = %+v, want one", relations)
	}
	accepted := relations[0]
	if accepted.Provenance != provenanceUserAccepted {
		t.Fatalf("provenance = %q, want %q", accepted.Provenance, provenanceUserAccepted)
	}
	if accepted.ValidFrom == nil {
		t.Fatalf("valid_from = nil, want now")
	}
	if accepted.Confidence == 0 {
		t.Fatalf("confidence = 0, want default 1.0")
	}

	after := getGraphNode(t, te, "task")
	for _, inf := range after.Inferred {
		if inf.EntityID == "cand" {
			t.Fatalf("accepted cand still in inferred: %+v", after.Inferred)
		}
	}
	foundEdge := false
	for _, group := range after.Edges {
		if group.Type != "mentions" {
			continue
		}
		for _, edge := range group.Edges {
			if edge.NeighborID == "cand" {
				foundEdge = true
			}
		}
	}
	if !foundEdge {
		t.Fatalf("accepted cand missing from verified edges: %+v", after.Edges)
	}
}

func TestGraphNode_HTMXAcceptReturnsRefreshedFocusViewer(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Task Node", Type: "task", Description: "mentions Cand and Cand2", SourceChunks: []string{"task-chunk"}},
		{ID: "cand", Name: "Cand", Type: "code", SourceChunks: []string{"cand-chunk"}},
		{ID: "cand2", Name: "Cand2", Type: "code", SourceChunks: []string{"cand2-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/graph/relations", strings.NewReader(url.Values{
		"src": {"task"}, "dst": {"cand"}, "type": {"mentions"},
		"provenance": {"user-accepted"}, "source_chunks": {"task-chunk"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	te.server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Task Node", "Connections", `id="node-cy"`, "Cand", "Suggested connections", "Cand2"} {
		if !strings.Contains(body, want) {
			t.Errorf("htmx accept response missing %q", want)
		}
	}
	if strings.Contains(body, "Add entity") || strings.Contains(body, "Entities") {
		t.Errorf("htmx accept response replaced viewer with admin list:\n%s", body)
	}
}

func TestGraphNode_AcceptReopensClosedRelation(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	past := time.Now().Add(-48 * time.Hour)
	closedTo := time.Now().Add(-24 * time.Hour)
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Task Node", Type: "task", Description: "mentions Cand", SourceChunks: []string{"task-chunk"}},
		{ID: "cand", Name: "Cand", Type: "code", SourceChunks: []string{"cand-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{{
		ID: graph.RelationID("task", "cand", "mentions"), Src: "task", Dst: "cand", Type: "mentions", Weight: 1,
		SourceChunks: []string{"task-chunk"}, ValidFrom: &past, ValidTo: &closedTo,
	}}); err != nil {
		t.Fatalf("UpsertRelations closed: %v", err)
	}

	before := getGraphNode(t, te, "task")
	found := false
	for _, inf := range before.Inferred {
		if inf.EntityID == "cand" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("closed relation pair must be suggested again as inferred: %+v", before.Inferred)
	}
	if len(before.Edges) != 0 {
		t.Fatalf("closed relation must not appear in verified edges: %+v", before.Edges)
	}

	rr := postForm(t, te.server.Handler(), "/graph/relations", url.Values{
		"src": {"task"}, "dst": {"cand"}, "type": {"mentions"},
		"provenance": {"user-accepted"}, "source_chunks": {"task-chunk"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	after := getGraphNode(t, te, "task")
	foundEdge := false
	for _, group := range after.Edges {
		if group.Type != "mentions" {
			continue
		}
		for _, edge := range group.Edges {
			if edge.NeighborID == "cand" {
				foundEdge = true
			}
		}
	}
	if !foundEdge {
		t.Fatalf("accepted cand missing from verified edges after reopen: %+v", after.Edges)
	}
	relations, err := te.graph.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 || relations[0].ValidTo != nil || relations[0].ExpiredAt != nil {
		t.Fatalf("relation after accept = %+v, want reopened (valid_to nil)", relations)
	}
}

func TestGraphNode_HTMXReturnsFocusViewer(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "task", Name: "Task Node", Type: "task", Description: "mentions Cand", SourceChunks: []string{"task-chunk"}},
		{ID: "cand", Name: "Cand", Type: "code", SourceChunks: []string{"cand-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.vector.Upsert(ctx, []vector.Chunk{{
		ID: "task-chunk", RefDocID: "notes/task", Text: "task body", Source: "notes",
		Metadata: map[string]string{"visibility": "approved"},
	}}); err != nil {
		t.Fatalf("vector Upsert: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/graph/node?id=task", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	te.server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content type = %q, want html", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Task Node", "Connections", "Edit entity", "найдено по тексту",
		`hx-post="/graph/relations"`, `name="provenance" value="user-accepted"`, `id="node-cy"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("focus viewer missing %q", want)
		}
	}
	if strings.Contains(body, "render error:") {
		t.Errorf("focus viewer render error: %s", body)
	}
}

func getGraphNode(t *testing.T, te *testEnv, id string) graphNodeResponse {
	t.Helper()
	rr := getPage(t, te.server.Handler(), "/graph/node?id="+id)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /graph/node?id=%s status = %d, want 200: %s", id, rr.Code, rr.Body.String())
	}
	var resp graphNodeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}
