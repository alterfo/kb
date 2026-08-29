package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/store/graphstore"
)

func TestRefreshGraphPropagatesRecomputeError(t *testing.T) {
	s := &Server{deps: Deps{GraphUpdater: graph.NewGraphUpdater(nil, nil, nil)}}
	if err := s.refreshGraph(context.Background(), nil); err == nil {
		t.Fatal("expected RecomputeCommunities error to propagate")
	}
}

func TestGraph_ShowsEntitiesRelationsAndCommunities(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e1", Name: "Alpha Corp", Type: "org", Degree: 2, SourceChunks: []string{"c1"}},
		{ID: "e2", Name: "Beta Ltd", Type: "org", Degree: 1, SourceChunks: []string{"c2"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r1", Src: "e1", Dst: "e2", Type: "owns", SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	if err := te.graph.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "c1", Title: "Media Cluster", Members: []string{"e1", "e2"}, Summary: "summary here"},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/graph")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Alpha Corp", "Beta Ltd", "Media Cluster", "summary here", `id="cy"`, "/static/cytoscape.min.js", "/graph/data"} {
		if !strings.Contains(body, want) {
			t.Errorf("graph page missing %q", want)
		}
	}
	if !strings.Contains(body, "Select an entity to inspect or edit it.") {
		t.Errorf("graph page missing entity panel placeholder: %q", body)
	}
	if strings.Contains(body, "render error:") {
		t.Errorf("graph page contains render error: %q", body)
	}
}

func TestGraph_DegradesWithoutStore(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{Root: root})
	rr := getPage(t, srv.Handler(), "/graph")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "knowledge graph is not configured") {
		t.Errorf("expected graph alert: %q", rr.Body.String())
	}
}

func TestGraphData_ReturnsNodesAndEdges(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e1", Name: "Alpha Corp", Type: "org", Degree: 2, SourceChunks: []string{"c1"}},
		{ID: "e2", Name: "Beta Ltd", Type: "org", Degree: 1, SourceChunks: []string{"c2"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r1", Src: "e1", Dst: "e2", Type: "owns", SourceChunks: []string{"c1"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/graph/data")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp graphDataResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(resp.Nodes))
	}
	if len(resp.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(resp.Edges))
	}
	if resp.TotalEntities != 2 || resp.ReturnedEntities != 2 || resp.Truncated {
		t.Errorf("unexpected totals: %+v", resp)
	}
}

func TestGraphData_FiltersByTypeAndMinDegreeAndQuery(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e1", Name: "Alpha Corp", Type: "org", Degree: 5, SourceChunks: []string{"c1"}},
		{ID: "e2", Name: "Beta Person", Type: "person", Degree: 1, SourceChunks: []string{"c2"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/graph/data?type=org")
	var resp graphDataResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].ID != "e1" {
		t.Fatalf("type filter = %+v, want only e1", resp.Nodes)
	}

	rr = getPage(t, te.server.Handler(), "/graph/data?min_degree=3")
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].ID != "e1" {
		t.Fatalf("min_degree filter = %+v, want only e1", resp.Nodes)
	}

	rr = getPage(t, te.server.Handler(), "/graph/data?q=beta")
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].ID != "e2" {
		t.Fatalf("q filter = %+v, want only e2", resp.Nodes)
	}
}

func TestGraphData_FiltersByCommunity(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e1", Name: "In Community", Type: "org", SourceChunks: []string{"c1"}},
		{ID: "e2", Name: "Outside", Type: "org", SourceChunks: []string{"c2"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "comm1", Title: "Cluster", Members: []string{"e1"}},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/graph/data?community=comm1")
	var resp graphDataResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].ID != "e1" {
		t.Fatalf("community filter = %+v, want only e1", resp.Nodes)
	}
	if resp.Nodes[0].Community != "comm1" {
		t.Errorf("node community = %q, want comm1", resp.Nodes[0].Community)
	}
}

func TestGraphData_LimitTruncatesAndDropsDanglingEdges(t *testing.T) {
	entities := []graphstore.Entity{
		{ID: "e1", Name: "A", Type: "org", Degree: 3, SourceChunks: []string{"c1"}},
		{ID: "e2", Name: "B", Type: "org", Degree: 2, SourceChunks: []string{"c2"}},
		{ID: "e3", Name: "C", Type: "org", Degree: 1, SourceChunks: []string{"c3"}},
	}
	relations := []graphstore.Relation{
		{ID: "r1", Src: "e1", Dst: "e2", Type: "rel"},
		{ID: "r2", Src: "e2", Dst: "e3", Type: "rel"},
	}
	resp := buildGraphData(entities, relations, nil, graphDataFilter{Limit: 2})
	if resp.ReturnedEntities != 2 || !resp.Truncated {
		t.Fatalf("expected truncated 2-entity result, got %+v", resp)
	}
	if resp.TotalEntities != 3 {
		t.Errorf("TotalEntities = %d, want 3", resp.TotalEntities)
	}
	// e1 (degree 3) and e2 (degree 2) survive the limit (highest degree
	// first); the r1 edge between them must be included, r2 (touching the
	// dropped e3) must not.
	if len(resp.Edges) != 1 || resp.Edges[0].ID != "r1" {
		t.Fatalf("edges = %+v, want only r1 (no dangling edge to a dropped node)", resp.Edges)
	}
}

func TestGraphData_EmptyGraph(t *testing.T) {
	te := newTestEnv(t, nil)
	rr := getPage(t, te.server.Handler(), "/graph/data")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp graphDataResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Nodes) != 0 || len(resp.Edges) != 0 || resp.Truncated {
		t.Errorf("expected empty response, got %+v", resp)
	}
}

func TestGraphData_DegradesWithoutStore(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Deps{Root: root})
	rr := getPage(t, srv.Handler(), "/graph/data")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestGraph_EntityListSortedByDegree(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "low", Name: "Low", Type: "org", Degree: 1, SourceChunks: []string{"low-chunk"}},
		{ID: "high", Name: "High", Type: "org", Degree: 9, SourceChunks: []string{"high-chunk"}},
		{ID: "mid", Name: "Mid", Type: "org", Degree: 5, SourceChunks: []string{"mid-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}

	rr := getPage(t, te.server.Handler(), "/graph/entities")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var rows []graphEntityView
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode entities: %v", err)
	}
	got := []string{rows[0].Name, rows[1].Name, rows[2].Name}
	want := []string{"High", "Mid", "Low"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entity order = %v, want %v", got, want)
		}
	}
}

func TestGraph_EntityUpsertAndDelete(t *testing.T) {
	te := newTestEnv(t, nil)

	rr := postJSON(t, te.server.Handler(), "/graph/entities", map[string]any{
		"name": "Alpha", "type": "org", "description": "an org", "source_chunks": []string{"alpha-chunk"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("upsert status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	entities, err := te.graph.AllEntities(context.Background())
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	if len(entities) != 1 || entities[0].Name != "Alpha" || entities[0].Description != "an org" {
		t.Fatalf("entities after upsert = %+v", entities)
	}

	rr = postJSON(t, te.server.Handler(), "/graph/entities", map[string]any{"name": "", "type": "org"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty name status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	rr = postJSON(t, te.server.Handler(), "/graph/entities", map[string]any{"name": "Alpha", "type": ""})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty type status = %d, want 400: %s", rr.Code, rr.Body.String())
	}

	panel := getPage(t, te.server.Handler(), "/graph/entity?id="+entities[0].ID)
	if panel.Code != http.StatusOK || !strings.Contains(panel.Body.String(), "an org") {
		t.Fatalf("entity panel status = %d, body = %s", panel.Code, panel.Body.String())
	}

	del := deleteRequest(t, te.server.Handler(), "/graph/entities?id="+entities[0].ID)
	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200: %s", del.Code, del.Body.String())
	}
	entities, err = te.graph.AllEntities(context.Background())
	if err != nil {
		t.Fatalf("AllEntities after delete: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("entities after delete = %+v, want empty", entities)
	}

	missing := deleteRequest(t, te.server.Handler(), "/graph/entities?id=nope")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing delete status = %d, want 404: %s", missing.Code, missing.Body.String())
	}
}

func TestGraph_RelationUpsertAndDelete(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e1", Name: "Alpha", Type: "org", SourceChunks: []string{"e1-chunk"}},
		{ID: "e2", Name: "Beta", Type: "org", SourceChunks: []string{"e2-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}

	rr := postJSON(t, te.server.Handler(), "/graph/relations", map[string]any{
		"src": "Alpha", "dst": "Beta", "type": "owns", "source_chunks": []string{"rel-chunk"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("relation upsert status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	relations, err := te.graph.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations: %v", err)
	}
	if len(relations) != 1 || relations[0].Type != "owns" {
		t.Fatalf("relations after upsert = %+v", relations)
	}

	rr = postJSON(t, te.server.Handler(), "/graph/relations", map[string]any{
		"src": "Missing", "dst": "Beta", "type": "owns",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing source status = %d, want 400: %s", rr.Code, rr.Body.String())
	}

	del := deleteRequest(t, te.server.Handler(), "/graph/relations?id="+relations[0].ID)
	if del.Code != http.StatusOK {
		t.Fatalf("relation delete status = %d, want 200: %s", del.Code, del.Body.String())
	}
	relations, err = te.graph.AllRelations(ctx)
	if err != nil {
		t.Fatalf("AllRelations after delete: %v", err)
	}
	if len(relations) != 0 {
		t.Fatalf("relations after delete = %+v, want empty", relations)
	}

	missing := deleteRequest(t, te.server.Handler(), "/graph/relations?id=nope")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing relation delete status = %d, want 404: %s", missing.Code, missing.Body.String())
	}
}

func TestGraph_EntityEditReplacesFields(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()

	rr := postJSON(t, te.server.Handler(), "/graph/entities", map[string]any{
		"name": "Alpha", "type": "org", "description": "old desc", "source_chunks": []string{"c1"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	entities, err := te.graph.AllEntities(ctx)
	if err != nil || len(entities) != 1 {
		t.Fatalf("entities after create = %+v, %v", entities, err)
	}
	id := entities[0].ID

	rr = postJSON(t, te.server.Handler(), "/graph/entities", map[string]any{
		"id": id, "name": "Alpha", "type": "org", "description": "", "source_chunks": []string{"c2"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("edit status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	entities, err = te.graph.AllEntities(ctx)
	if err != nil || len(entities) != 1 {
		t.Fatalf("entities after edit = %+v, %v", entities, err)
	}
	if entities[0].Description != "" {
		t.Fatalf("description = %q, want cleared by manual edit", entities[0].Description)
	}
	if got := strings.Join(entities[0].SourceChunks, ","); got != "c2" {
		t.Fatalf("source_chunks = %q, want c2 (replaced, not unioned)", got)
	}
}

func TestGraph_RelationEditReplacesWeight(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e1", Name: "Alpha", Type: "org", SourceChunks: []string{"e1-chunk"}},
		{ID: "e2", Name: "Beta", Type: "org", SourceChunks: []string{"e2-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}

	rr := postJSON(t, te.server.Handler(), "/graph/relations", map[string]any{
		"src": "Alpha", "dst": "Beta", "type": "owns", "weight": 1,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	rr = postJSON(t, te.server.Handler(), "/graph/relations", map[string]any{
		"src": "Alpha", "dst": "Beta", "type": "owns", "weight": 5,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("edit status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	relations, err := te.graph.AllRelations(ctx)
	if err != nil || len(relations) != 1 {
		t.Fatalf("relations after edit = %+v, %v", relations, err)
	}
	if relations[0].Weight != 5 {
		t.Fatalf("weight = %v, want 5 (replaced, not summed)", relations[0].Weight)
	}
}

func TestGraph_DeleteRecomputesCommunities(t *testing.T) {
	te := newTestEnv(t, nil)
	ctx := context.Background()
	if err := te.graph.UpsertEntities(ctx, []graphstore.Entity{
		{ID: "e1", Name: "Alpha", Type: "org", SourceChunks: []string{"e1-chunk"}},
		{ID: "e2", Name: "Beta", Type: "org", SourceChunks: []string{"e2-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if err := te.graph.UpsertRelations(ctx, []graphstore.Relation{
		{ID: "r1", Src: "e1", Dst: "e2", Type: "owns", SourceChunks: []string{"rel-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	if err := te.graph.UpsertCommunities(ctx, []graphstore.Community{
		{ID: "c1", Members: []string{"e1", "e2"}, Title: "Cluster", SourceChunks: []string{"rel-chunk"}},
	}); err != nil {
		t.Fatalf("UpsertCommunities: %v", err)
	}

	rr := deleteRequest(t, te.server.Handler(), "/graph/entities?id=e1")
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	communities, err := te.graph.AllCommunities(ctx)
	if err != nil {
		t.Fatalf("AllCommunities: %v", err)
	}
	for _, c := range communities {
		for _, member := range c.Members {
			if member == "e1" {
				t.Fatalf("community still contains deleted entity: %+v", c)
			}
		}
	}
	remaining, err := te.graph.CommunitiesFor(ctx, []string{"e2"})
	if err != nil {
		t.Fatalf("CommunitiesFor: %v", err)
	}
	if len(remaining) == 0 {
		t.Fatalf("expected a recomputed community for surviving entity e2")
	}
}

func deleteRequest(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestGraphData_FiltersByAnyCommunityLevel(t *testing.T) {
	entities := []graphstore.Entity{
		{ID: "e1", Name: "Nested", Type: "org", Degree: 1, SourceChunks: []string{"c1"}},
		{ID: "e2", Name: "Sibling", Type: "org", Degree: 1, SourceChunks: []string{"c2"}},
	}
	communities := []graphstore.Community{
		{ID: "top", Level: 0, Members: []string{"e1", "e2"}},
		{ID: "leaf", Level: 1, Members: []string{"e1"}},
	}

	resp := buildGraphData(entities, nil, communities, graphDataFilter{Community: "leaf"})
	if len(resp.Nodes) != 1 || resp.Nodes[0].ID != "e1" {
		t.Fatalf("leaf community filter = %+v, want only e1", resp.Nodes)
	}
	resp = buildGraphData(entities, nil, communities, graphDataFilter{Community: "top"})
	if len(resp.Nodes) != 2 {
		t.Fatalf("top community filter = %+v, want both e1 and e2", resp.Nodes)
	}
}
