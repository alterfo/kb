package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

type graphEntityView struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Description  string   `json:"description,omitempty"`
	Degree       int      `json:"degree"`
	SourceChunks []string `json:"source_chunks,omitempty"`
	DetailURL    string   `json:"-"`
	DeleteURL    string   `json:"-"`
}

type graphRelationView struct {
	ID          string  `json:"id"`
	Src         string  `json:"src"`
	Dst         string  `json:"dst"`
	Type        string  `json:"type"`
	Description string  `json:"description,omitempty"`
	Weight      float64 `json:"weight,omitempty"`
	DeleteURL   string  `json:"-"`
}

type graphCommunityView struct {
	ID      string
	Title   string
	Summary string
	Members []string
}

type graphEntityDetail struct {
	ID           string
	Name         string
	Type         string
	Description  string
	Degree       int
	SourceChunks []string
	Relations    []graphRelationView
	DeleteURL    string
}

type graphData struct {
	Entities    []graphEntityView
	Relations   []graphRelationView
	Communities []graphCommunityView
	Selected    *graphEntityDetail
}

var palette = []string{"#4f8cff", "#34c77b", "#e5a63b", "#e5534b", "#a36bff", "#2fb8c9", "#f078a8", "#b8c14a"}

func graphEntityURL(id string) string {
	return "/graph/entity?id=" + url.QueryEscape(id)
}

func graphEntityDeleteURL(id string) string {
	return "/graph/entities?id=" + url.QueryEscape(id)
}

func graphRelationDeleteURL(id string) string {
	return "/graph/relations?id=" + url.QueryEscape(id)
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if s.deps.Graph == nil {
		s.render(w, "page-graph", http.StatusOK, page{
			Title:  "Knowledge graph",
			Alerts: []Alert{{Kind: "error", Message: "knowledge graph is not configured"}},
			Data:   graphData{},
		})
		return
	}
	data, err := s.graphView(r.Context())
	if err != nil {
		s.render(w, "page-graph", http.StatusOK, page{
			Title:  "Knowledge graph",
			Alerts: []Alert{{Kind: "error", Message: "loading graph failed: " + err.Error()}},
			Data:   graphData{},
		})
		return
	}
	s.render(w, "page-graph", http.StatusOK, page{Title: "Knowledge graph", Data: data})
}

func (s *Server) graphView(ctx context.Context) (graphData, error) {
	if s.deps.Graph == nil {
		return graphData{}, errors.New("knowledge graph is not configured")
	}
	entities, err := s.deps.Graph.AllEntities(ctx)
	if err != nil {
		return graphData{}, err
	}
	relations, err := s.deps.Graph.AllRelations(ctx)
	if err != nil {
		return graphData{}, err
	}
	communities, err := s.deps.Graph.AllCommunities(ctx)
	if err != nil {
		return graphData{}, err
	}
	return buildGraphView(entities, relations, communities), nil
}

func sortGraphEntities(entities []graphstore.Entity) {
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].Degree != entities[j].Degree {
			return entities[i].Degree > entities[j].Degree
		}
		return entities[i].Name < entities[j].Name
	})
}

func buildGraphView(entities []graphstore.Entity, relations []graphstore.Relation, communities []graphstore.Community) graphData {
	data := graphData{}
	sortGraphEntities(entities)
	sort.Slice(relations, func(i, j int) bool {
		if relations[i].Src != relations[j].Src {
			return relations[i].Src < relations[j].Src
		}
		if relations[i].Type != relations[j].Type {
			return relations[i].Type < relations[j].Type
		}
		return relations[i].Dst < relations[j].Dst
	})

	for _, c := range communities {
		title := c.Title
		if strings.TrimSpace(title) == "" {
			title = c.ID
		}
		data.Communities = append(data.Communities, graphCommunityView{
			ID: c.ID, Title: title, Summary: c.Summary, Members: c.Members,
		})
	}

	for _, rel := range relations {
		data.Relations = append(data.Relations, graphRelationView{
			ID: rel.ID, Src: rel.Src, Dst: rel.Dst, Type: rel.Type,
			Description: rel.Description, Weight: rel.Weight,
			DeleteURL: graphRelationDeleteURL(rel.ID),
		})
	}

	for _, e := range entities {
		data.Entities = append(data.Entities, graphEntityView{
			ID: e.ID, Name: e.Name, Type: e.Type, Description: e.Description,
			Degree: e.Degree, SourceChunks: e.SourceChunks,
			DetailURL: graphEntityURL(e.ID), DeleteURL: graphEntityDeleteURL(e.ID),
		})
	}
	return data
}

// graphNodeJSON/graphEdgeJSON are the /graph/data wire format consumed by
// the client-side Cytoscape renderer (templates/graph.html).
type graphNodeJSON struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Type      string `json:"type"`
	Degree    int    `json:"degree"`
	Color     string `json:"color"`
	Community string `json:"community,omitempty"`
}

type graphEdgeJSON struct {
	ID     string  `json:"id"`
	Source string  `json:"source"`
	Target string  `json:"target"`
	Type   string  `json:"type"`
	Weight float64 `json:"weight,omitempty"`
}

type graphDataResponse struct {
	Nodes            []graphNodeJSON `json:"nodes"`
	Edges            []graphEdgeJSON `json:"edges"`
	TotalEntities    int             `json:"total_entities"`
	TotalRelations   int             `json:"total_relations"`
	ReturnedEntities int             `json:"returned_entities"`
	Truncated        bool            `json:"truncated"`
}

type graphNodeDocument struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Description  string   `json:"description,omitempty"`
	SourceChunks []string `json:"source_chunks,omitempty"`
}

type graphNodeEdge struct {
	ID               string   `json:"id"`
	Source           string   `json:"source"`
	Target           string   `json:"target"`
	Type             string   `json:"type"`
	Description      string   `json:"description,omitempty"`
	Weight           float64  `json:"weight,omitempty"`
	Confidence       float64  `json:"confidence"`
	Provenance       string   `json:"provenance,omitempty"`
	ExtractorVersion string   `json:"extractor_version,omitempty"`
	NeighborID       string   `json:"neighbor_id"`
	NeighborName     string   `json:"neighbor_name"`
	NeighborType     string   `json:"neighbor_type,omitempty"`
	Authority        string   `json:"authority,omitempty"`
	Recency          string   `json:"recency,omitempty"`
	SourceChunks     []string `json:"-"`
}

type graphNodeEdgeGroup struct {
	Type     string          `json:"type"`
	Count    int             `json:"count"`
	Foldable bool            `json:"foldable"`
	Edges    []graphNodeEdge `json:"edges"`
}

type graphNodeCommunity struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type graphNodeInferred struct {
	EntityID     string   `json:"entity_id"`
	EntityName   string   `json:"entity_name"`
	EntityType   string   `json:"entity_type,omitempty"`
	Type         string   `json:"type"`
	Provenance   string   `json:"provenance"`
	SourceChunks []string `json:"source_chunks,omitempty"`
}

type graphNodeResponse struct {
	Document       graphNodeDocument    `json:"document"`
	TotalNeighbors int                  `json:"total_neighbors"`
	Hub            bool                 `json:"hub,omitempty"`
	Edges          []graphNodeEdgeGroup `json:"edges"`
	Community      []graphNodeCommunity `json:"community,omitempty"`
	Inferred       []graphNodeInferred  `json:"inferred,omitempty"`
	Warnings       []string             `json:"warnings,omitempty"`
}

const (
	defaultGraphNodeLimit      = 10
	maxGraphNodeLimit          = 100
	defaultGraphNodeInferBelow = 5
	maxGraphNodeInferred       = 10
	graphNodeDenseThreshold    = 20
	graphNodeInferredType      = "mentions"
	provenanceEntityLinking    = "entity-linking"
	provenanceUserAccepted     = "user-accepted"
)

type graphNodeParams struct {
	Limit         int
	MinConfidence float64
	InferBelow    int
}

func parseGraphNodeParams(r *http.Request) graphNodeParams {
	p := graphNodeParams{
		Limit:      defaultGraphNodeLimit,
		InferBelow: defaultGraphNodeInferBelow,
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			p.Limit = n
		}
	}
	if p.Limit > maxGraphNodeLimit {
		p.Limit = maxGraphNodeLimit
	}
	if raw := r.URL.Query().Get("min_confidence"); raw != "" {
		if n, err := strconv.ParseFloat(raw, 64); err == nil {
			p.MinConfidence = n
		}
	}
	if raw := r.URL.Query().Get("infer_below"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			p.InferBelow = n
		}
	}
	return p
}

const (
	defaultGraphDataLimit = 200
	maxGraphDataLimit     = 1000
)

type graphDataFilter struct {
	Query     string
	Community string
	Type      string
	MinDegree int
	Limit     int
}

func parseGraphDataFilter(r *http.Request) graphDataFilter {
	f := graphDataFilter{
		Query:     strings.TrimSpace(r.URL.Query().Get("q")),
		Community: strings.TrimSpace(r.URL.Query().Get("community")),
		Type:      strings.TrimSpace(r.URL.Query().Get("type")),
		Limit:     defaultGraphDataLimit,
	}
	if raw := r.URL.Query().Get("min_degree"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			f.MinDegree = n
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			f.Limit = n
		}
	}
	if f.Limit > maxGraphDataLimit {
		f.Limit = maxGraphDataLimit
	}
	return f
}

// buildGraphData filters/paginates entities+relations for the client-side
// graph canvas. Unlike buildGraphView (used by the CRUD tables, which
// always lists everything), this caps the node count so a large corpus
// stays interactively renderable — Truncated tells the client the result
// isn't the whole graph, so it can say so instead of silently looking
// complete.
func buildGraphData(entities []graphstore.Entity, relations []graphstore.Relation, communities []graphstore.Community, filter graphDataFilter) graphDataResponse {
	sortGraphEntities(entities)

	communityOf := map[string]string{}
	communityColor := map[string]string{}
	communityMembers := map[string]map[string]bool{}
	for i, c := range communities {
		color := palette[i%len(palette)]
		for _, m := range c.Members {
			if communityOf[m] == "" {
				communityOf[m] = c.ID
				communityColor[m] = color
			}
			if communityMembers[m] == nil {
				communityMembers[m] = map[string]bool{}
			}
			communityMembers[m][c.ID] = true
		}
	}

	q := strings.ToLower(filter.Query)
	matched := make([]graphstore.Entity, 0, len(entities))
	for _, e := range entities {
		if filter.Type != "" && e.Type != filter.Type {
			continue
		}
		if filter.MinDegree > 0 && e.Degree < filter.MinDegree {
			continue
		}
		if filter.Community != "" && !communityMembers[e.ID][filter.Community] {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Name), q) && !strings.Contains(strings.ToLower(e.Description), q) {
			continue
		}
		matched = append(matched, e)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultGraphDataLimit
	}
	truncated := len(matched) > limit
	returned := matched
	if truncated {
		returned = matched[:limit]
	}

	included := make(map[string]struct{}, len(returned))
	resp := graphDataResponse{
		TotalEntities:    len(entities),
		TotalRelations:   len(relations),
		ReturnedEntities: len(returned),
		Truncated:        truncated,
	}
	for _, e := range returned {
		included[e.ID] = struct{}{}
		color := communityColor[e.ID]
		if color == "" {
			color = "#8fa0bd"
		}
		resp.Nodes = append(resp.Nodes, graphNodeJSON{
			ID: e.ID, Label: e.Name, Type: e.Type, Degree: e.Degree,
			Color: color, Community: communityOf[e.ID],
		})
	}
	for _, rel := range relations {
		if _, ok := included[rel.Src]; !ok {
			continue
		}
		if _, ok := included[rel.Dst]; !ok {
			continue
		}
		resp.Edges = append(resp.Edges, graphEdgeJSON{
			ID: rel.ID, Source: rel.Src, Target: rel.Dst, Type: rel.Type, Weight: rel.Weight,
		})
	}
	return resp
}

func (s *Server) handleGraphData(w http.ResponseWriter, r *http.Request) {
	if s.deps.Graph == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "knowledge graph is not configured"})
		return
	}
	ctx := r.Context()
	entities, err := s.deps.Graph.AllEntities(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	relations, err := s.deps.Graph.AllRelations(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	communities, err := s.deps.Graph.AllCommunities(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, buildGraphData(entities, relations, communities, parseGraphDataFilter(r)))
}

func (s *Server) handleGraphNode(w http.ResponseWriter, r *http.Request) {
	if s.deps.Graph == nil {
		if isHtmx(r) {
			s.render(w, "node-view", http.StatusOK, page{Title: "Node", Data: graphNodePage{graphNodeResponse: graphNodeResponse{
				Edges:    make([]graphNodeEdgeGroup, 0),
				Warnings: []string{"knowledge graph is not configured"},
			}}})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, graphNodeResponse{
			Edges:    make([]graphNodeEdgeGroup, 0),
			Warnings: []string{"knowledge graph is not configured"},
		})
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("id"))
	if ref == "" {
		ref = strings.TrimSpace(r.URL.Query().Get("name"))
	}
	if ref == "" {
		if isHtmx(r) {
			s.render(w, "node-view", http.StatusOK, page{Title: "Node", Data: graphNodePage{graphNodeResponse: graphNodeResponse{
				Edges:    make([]graphNodeEdgeGroup, 0),
				Warnings: []string{"entity id or name is required"},
			}}})
			return
		}
		writeJSON(w, http.StatusBadRequest, graphNodeResponse{
			Edges:    make([]graphNodeEdgeGroup, 0),
			Warnings: []string{"entity id or name is required"},
		})
		return
	}
	entity, err := s.findEntity(r.Context(), ref)
	if err != nil {
		if isHtmx(r) {
			s.render(w, "node-view", http.StatusOK, page{Title: "Node", Data: graphNodePage{graphNodeResponse: graphNodeResponse{
				Edges:    make([]graphNodeEdgeGroup, 0),
				Warnings: []string{"loading entity failed: " + err.Error()},
			}}})
			return
		}
		writeJSON(w, http.StatusOK, graphNodeResponse{
			Edges:    make([]graphNodeEdgeGroup, 0),
			Warnings: []string{"loading entity failed: " + err.Error()},
		})
		return
	}
	if isHtmx(r) {
		s.render(w, "node-view", http.StatusOK, page{Title: entity.Name, Data: s.buildGraphNodePage(r.Context(), entity, parseGraphNodeParams(r))})
		return
	}
	writeJSON(w, http.StatusOK, s.buildGraphNode(r.Context(), entity, parseGraphNodeParams(r)))
}

func (s *Server) buildGraphNode(ctx context.Context, entity graphstore.Entity, params graphNodeParams) graphNodeResponse {
	resp := graphNodeResponse{
		Document: graphNodeDocument{
			ID:           entity.ID,
			Name:         entity.Name,
			Type:         entity.Type,
			Description:  entity.Description,
			SourceChunks: entity.SourceChunks,
		},
		Edges: make([]graphNodeEdgeGroup, 0),
	}

	neighbors, relations, err := s.deps.Graph.Neighbors(ctx, entity.ID, 1)
	if err != nil {
		resp.Warnings = append(resp.Warnings, "loading neighbors failed: "+err.Error())
		return resp
	}
	neighborsByID := make(map[string]graphstore.Entity, len(neighbors))
	for _, neighbor := range neighbors {
		neighborsByID[neighbor.ID] = neighbor
	}
	resp.TotalNeighbors = len(neighbors)
	resp.Hub = len(neighbors) > graphNodeDenseThreshold
	chunks := s.chunkIndex(ctx)
	resp.Edges = buildGraphNodeEdgeGroups(entity.ID, relations, neighborsByID, params.MinConfidence, params.Limit, chunks, s.deps.Now())
	s.enrichNodeEdgeGroups(ctx, resp.Edges, chunks)

	communities, err := s.deps.Graph.CommunitiesFor(ctx, []string{entity.ID})
	if err != nil {
		resp.Warnings = append(resp.Warnings, "loading communities failed: "+err.Error())
	} else {
		resp.Community = buildGraphNodeCommunities(communities)
	}

	if len(neighbors) < params.InferBelow {
		resp.Inferred = s.buildInferredNodeEdges(ctx, entity, neighborsByID, maxGraphNodeInferred)
	}
	return resp
}

type graphNodePage struct {
	graphNodeResponse
	DocPath    string
	DocTitle   string
	DocBody    template.HTML
	DocViewURL string
}

func (s *Server) buildGraphNodePage(ctx context.Context, entity graphstore.Entity, params graphNodeParams) graphNodePage {
	p := graphNodePage{graphNodeResponse: s.buildGraphNode(ctx, entity, params)}
	if path := s.entityDocPath(ctx, entity.SourceChunks); path != "" {
		if doc, err := s.readDoc(path); err == nil {
			p.DocPath = doc.Path
			p.DocTitle = doc.Title
			p.DocBody = doc.Body
			p.DocViewURL = "/documents/view?path=" + url.QueryEscape(doc.Path)
		}
	}
	return p
}

func (s *Server) entityDocPath(ctx context.Context, chunkIDs []string) string {
	chunks := s.chunkIndex(ctx)
	for _, chunkID := range chunkIDs {
		if chunk, ok := chunks[chunkID]; ok && strings.TrimSpace(chunk.FilePath) != "" {
			return chunk.FilePath
		}
	}
	return ""
}

type nodeEdgeCandidate struct {
	relation   graphstore.Relation
	neighborID string
	age        time.Duration
}

func buildGraphNodeEdgeGroups(nodeID string, relations []graphstore.Relation, neighborsByID map[string]graphstore.Entity, minConfidence float64, limit int, chunks map[string]vector.Chunk, now time.Time) []graphNodeEdgeGroup {
	if limit <= 0 {
		limit = defaultGraphNodeLimit
	}
	candidates := make([]nodeEdgeCandidate, 0)
	for _, relation := range relations {
		if relation.Src != nodeID && relation.Dst != nodeID {
			continue
		}
		neighborID := relation.Src
		if neighborID == nodeID {
			neighborID = relation.Dst
		}
		if neighborID == nodeID {
			continue
		}
		if relation.Confidence < minConfidence {
			continue
		}
		_, age := relationReportScore(relation.SourceChunks, chunks, now)
		candidates = append(candidates, nodeEdgeCandidate{relation: relation, neighborID: neighborID, age: age})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].age != candidates[j].age {
			return candidates[i].age < candidates[j].age
		}
		return candidates[i].neighborID < candidates[j].neighborID
	})

	grouped := make(map[string][]nodeEdgeCandidate)
	for _, candidate := range candidates {
		grouped[candidate.relation.Type] = append(grouped[candidate.relation.Type], candidate)
	}

	groups := make([]graphNodeEdgeGroup, 0, len(grouped))
	for relationType, groupCandidates := range grouped {
		group := graphNodeEdgeGroup{
			Type:  relationType,
			Count: len(groupCandidates),
			Edges: make([]graphNodeEdge, 0, min(len(groupCandidates), limit)),
		}
		if len(groupCandidates) > limit {
			group.Foldable = true
			groupCandidates = groupCandidates[:limit]
		}
		for _, candidate := range groupCandidates {
			group.Edges = append(group.Edges, nodeEdgeView(candidate.relation, candidate.neighborID, neighborsByID[candidate.neighborID]))
		}
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Type < groups[j].Type
	})
	return groups
}

func nodeEdgeView(relation graphstore.Relation, neighborID string, neighbor graphstore.Entity) graphNodeEdge {
	neighborName := neighbor.Name
	if neighborName == "" {
		neighborName = neighborID
	}
	return graphNodeEdge{
		ID:               relation.ID,
		Source:           relation.Src,
		Target:           relation.Dst,
		Type:             relation.Type,
		Description:      relation.Description,
		Weight:           relation.Weight,
		Confidence:       relation.Confidence,
		Provenance:       relation.Provenance,
		ExtractorVersion: relation.ExtractorVersion,
		NeighborID:       neighborID,
		NeighborName:     neighborName,
		NeighborType:     neighbor.Type,
		SourceChunks:     relation.SourceChunks,
	}
}

func buildGraphNodeCommunities(communities []graphstore.Community) []graphNodeCommunity {
	out := make([]graphNodeCommunity, 0, len(communities))
	for _, community := range communities {
		title := community.Title
		if strings.TrimSpace(title) == "" {
			title = community.ID
		}
		out = append(out, graphNodeCommunity{ID: community.ID, Title: title, Summary: community.Summary})
	}
	return out
}

func (s *Server) enrichNodeEdgeGroups(ctx context.Context, groups []graphNodeEdgeGroup, chunks map[string]vector.Chunk) {
	if len(groups) == 0 {
		return
	}
	now := s.deps.Now()
	for gi := range groups {
		for ei := range groups[gi].Edges {
			groups[gi].Edges[ei].Authority, groups[gi].Edges[ei].Recency = edgeExplain(groups[gi].Edges[ei].SourceChunks, chunks, now)
		}
	}
}

func (s *Server) chunkIndex(ctx context.Context) map[string]vector.Chunk {
	out := map[string]vector.Chunk{}
	if s.deps.Vector == nil {
		return out
	}
	chunks, err := s.deps.Vector.AllForBM25(ctx)
	if err != nil {
		return out
	}
	for _, chunk := range chunks {
		out[chunk.ID] = chunk
	}
	return out
}

func edgeExplain(sourceChunks []string, chunks map[string]vector.Chunk, now time.Time) (authority, recency string) {
	rank := 2
	age := time.Duration(1<<63 - 1)
	for _, chunkID := range sourceChunks {
		chunk, ok := chunks[chunkID]
		if !ok {
			continue
		}
		if visibility := strings.TrimSpace(chunk.Metadata["visibility"]); visibility != "" {
			if r := authorityRank(visibility); r < rank {
				rank = r
				authority = visibility
			}
		}
		if chunk.CreatedAt != "" {
			if createdAt, err := time.Parse(time.RFC3339Nano, chunk.CreatedAt); err == nil {
				since := now.Sub(createdAt)
				if since < 0 {
					since = 0
				}
				if since < age {
					age = since
				}
			}
		}
	}
	if age != time.Duration(1<<63-1) {
		recency = humanRecency(age)
	}
	return authority, recency
}

func humanRecency(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (s *Server) buildInferredNodeEdges(ctx context.Context, entity graphstore.Entity, neighborsByID map[string]graphstore.Entity, limit int) []graphNodeInferred {
	if limit <= 0 {
		limit = maxGraphNodeInferred
	}
	text := s.nodeLinkingText(ctx, entity)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	linked := retriever.LinkEntities(ctx, s.deps.Graph, text)
	sort.Slice(linked, func(i, j int) bool { return linked[i].Name < linked[j].Name })

	seen := make(map[string]struct{}, len(neighborsByID)+1)
	seen[entity.ID] = struct{}{}
	for neighborID := range neighborsByID {
		seen[neighborID] = struct{}{}
	}

	out := make([]graphNodeInferred, 0, min(len(linked), limit))
	for _, linkedEntity := range linked {
		if linkedEntity.ID == entity.ID {
			continue
		}
		if _, ok := seen[linkedEntity.ID]; ok {
			continue
		}
		seen[linkedEntity.ID] = struct{}{}
		out = append(out, graphNodeInferred{
			EntityID:     linkedEntity.ID,
			EntityName:   linkedEntity.Name,
			EntityType:   linkedEntity.Type,
			Type:         graphNodeInferredType,
			Provenance:   provenanceEntityLinking,
			SourceChunks: entity.SourceChunks,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Server) nodeLinkingText(ctx context.Context, entity graphstore.Entity) string {
	parts := make([]string, 0, len(entity.SourceChunks)+2)
	if strings.TrimSpace(entity.Name) != "" {
		parts = append(parts, entity.Name)
	}
	if strings.TrimSpace(entity.Description) != "" {
		parts = append(parts, entity.Description)
	}
	for _, text := range s.chunkTexts(ctx, entity.SourceChunks) {
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func (s *Server) chunkTexts(ctx context.Context, chunkIDs []string) []string {
	if len(chunkIDs) == 0 {
		return nil
	}
	if s.deps.Vector != nil {
		chunks, err := s.deps.Vector.AllForBM25(ctx)
		if err == nil {
			byID := make(map[string]string, len(chunks))
			for _, chunk := range chunks {
				byID[chunk.ID] = chunk.Text
			}
			out := make([]string, 0, len(chunkIDs))
			for _, chunkID := range chunkIDs {
				if text, ok := byID[chunkID]; ok {
					out = append(out, text)
				}
			}
			return out
		}
	}
	if s.deps.BM25 != nil {
		out := make([]string, 0, len(chunkIDs))
		for _, chunkID := range chunkIDs {
			if chunk, ok := s.deps.BM25.Chunk(chunkID); ok {
				out = append(out, chunk.Text)
			}
		}
		return out
	}
	return nil
}

func (s *Server) handleGraphEntitiesList(w http.ResponseWriter, r *http.Request) {
	if s.deps.Graph == nil {
		s.writeGraphFailure(w, r, http.StatusServiceUnavailable, "knowledge graph is not configured")
		return
	}
	entities, err := s.deps.Graph.AllEntities(r.Context())
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "listing entities failed: "+err.Error())
		return
	}
	sortGraphEntities(entities)
	rows := make([]graphEntityView, 0, len(entities))
	for _, e := range entities {
		row := graphEntityView{
			ID: e.ID, Name: e.Name, Type: e.Type, Description: e.Description,
			Degree: e.Degree, SourceChunks: e.SourceChunks,
			DetailURL: graphEntityURL(e.ID), DeleteURL: graphEntityDeleteURL(e.ID),
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleGraphEntityPanel(w http.ResponseWriter, r *http.Request) {
	if s.deps.Graph == nil {
		s.writeGraphFailure(w, r, http.StatusServiceUnavailable, "knowledge graph is not configured")
		return
	}
	ref := r.URL.Query().Get("id")
	if ref == "" {
		ref = r.URL.Query().Get("name")
	}
	if ref == "" {
		s.writeGraphFailure(w, r, http.StatusBadRequest, "entity id or name is required")
		return
	}
	entity, err := s.findEntity(r.Context(), ref)
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusNotFound, err.Error())
		return
	}
	detail, err := s.entityDetail(r.Context(), entity)
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.render(w, "entity-panel", http.StatusOK, page{Title: entity.Name, Data: graphData{Selected: &detail}})
}

func (s *Server) entityDetail(ctx context.Context, entity graphstore.Entity) (graphEntityDetail, error) {
	relations, err := s.deps.Graph.AllRelations(ctx)
	if err != nil {
		return graphEntityDetail{}, err
	}
	sort.Slice(relations, func(i, j int) bool {
		if relations[i].Type != relations[j].Type {
			return relations[i].Type < relations[j].Type
		}
		if relations[i].Src != relations[j].Src {
			return relations[i].Src < relations[j].Src
		}
		return relations[i].Dst < relations[j].Dst
	})
	detail := graphEntityDetail{
		ID: entity.ID, Name: entity.Name, Type: entity.Type, Description: entity.Description,
		Degree: entity.Degree, SourceChunks: entity.SourceChunks,
		DeleteURL: graphEntityDeleteURL(entity.ID),
	}
	for _, rel := range relations {
		if rel.Src != entity.ID && rel.Dst != entity.ID {
			continue
		}
		detail.Relations = append(detail.Relations, graphRelationView{
			ID: rel.ID, Src: rel.Src, Dst: rel.Dst, Type: rel.Type,
			Description: rel.Description, Weight: rel.Weight,
			DeleteURL: graphRelationDeleteURL(rel.ID),
		})
	}
	return detail, nil
}

type graphEntityPayload struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Description  string   `json:"description"`
	SourceChunks []string `json:"source_chunks"`
}

func parseGraphEntityPayload(r *http.Request) (graphEntityPayload, error) {
	var p graphEntityPayload
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			return p, fmt.Errorf("invalid json: %w", err)
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return p, err
		}
		p.ID = strings.TrimSpace(r.FormValue("id"))
		p.Name = strings.TrimSpace(r.FormValue("name"))
		p.Type = strings.TrimSpace(r.FormValue("type"))
		p.Description = strings.TrimSpace(r.FormValue("description"))
		p.SourceChunks = splitSourceChunks(r.FormValue("source_chunks"))
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Type = strings.TrimSpace(p.Type)
	p.ID = strings.TrimSpace(p.ID)
	return p, nil
}

func (p graphEntityPayload) validated() (graphstore.Entity, error) {
	if p.Name == "" {
		return graphstore.Entity{}, errors.New("entity name is required")
	}
	if p.Type == "" {
		return graphstore.Entity{}, errors.New("entity type is required")
	}
	id := p.ID
	if id == "" {
		id = graph.EntityID(p.Name, p.Type)
	} else if id != graph.EntityID(p.Name, p.Type) {
		return graphstore.Entity{}, errors.New("renaming or changing the type of an existing entity is not supported; delete it and create a new entity instead")
	}
	chunks, err := chunksWithDefault(p.SourceChunks, "manual:"+id)
	if err != nil {
		return graphstore.Entity{}, err
	}
	return graphstore.Entity{
		ID: id, Name: p.Name, Type: p.Type, Description: p.Description, SourceChunks: chunks,
	}, nil
}

type graphRelationPayload struct {
	ID           string   `json:"id"`
	Src          string   `json:"src"`
	Dst          string   `json:"dst"`
	Type         string   `json:"type"`
	Description  string   `json:"description"`
	Weight       float64  `json:"weight"`
	Confidence   float64  `json:"confidence"`
	Provenance   string   `json:"provenance"`
	ValidFrom    string   `json:"valid_from"`
	SourceChunks []string `json:"source_chunks"`
}

func parseGraphRelationPayload(r *http.Request) (graphRelationPayload, error) {
	var p graphRelationPayload
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			return p, fmt.Errorf("invalid json: %w", err)
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return p, err
		}
		p.ID = strings.TrimSpace(r.FormValue("id"))
		p.Src = strings.TrimSpace(r.FormValue("src"))
		p.Dst = strings.TrimSpace(r.FormValue("dst"))
		p.Type = strings.TrimSpace(r.FormValue("type"))
		p.Description = strings.TrimSpace(r.FormValue("description"))
		p.SourceChunks = splitSourceChunks(r.FormValue("source_chunks"))
		p.Provenance = strings.TrimSpace(r.FormValue("provenance"))
		p.ValidFrom = strings.TrimSpace(r.FormValue("valid_from"))
		if raw := strings.TrimSpace(r.FormValue("weight")); raw != "" {
			weight, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return p, fmt.Errorf("invalid weight: %w", err)
			}
			p.Weight = weight
		}
		if raw := strings.TrimSpace(r.FormValue("confidence")); raw != "" {
			confidence, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return p, fmt.Errorf("invalid confidence: %w", err)
			}
			p.Confidence = confidence
		}
	}
	p.Src = strings.TrimSpace(p.Src)
	p.Dst = strings.TrimSpace(p.Dst)
	p.Type = strings.TrimSpace(p.Type)
	p.ID = strings.TrimSpace(p.ID)
	p.Provenance = strings.TrimSpace(p.Provenance)
	p.ValidFrom = strings.TrimSpace(p.ValidFrom)
	return p, nil
}

func (s *Server) handleGraphEntityUpsert(w http.ResponseWriter, r *http.Request) {
	if s.deps.Graph == nil {
		s.writeGraphFailure(w, r, http.StatusServiceUnavailable, "knowledge graph is not configured")
		return
	}
	payload, err := parseGraphEntityPayload(r)
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusBadRequest, err.Error())
		return
	}
	entity, err := payload.validated()
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Graph.PutEntity(r.Context(), entity); err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "save entity failed: "+err.Error())
		return
	}
	if err := s.refreshGraph(r.Context(), []string{entity.ID}); err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "refreshing graph failed: "+err.Error())
		return
	}
	s.writeGraphMutationSuccess(w, r, entity)
}

func (s *Server) handleGraphEntityDelete(w http.ResponseWriter, r *http.Request) {
	if s.deps.Graph == nil {
		s.writeGraphFailure(w, r, http.StatusServiceUnavailable, "knowledge graph is not configured")
		return
	}
	ref := r.URL.Query().Get("id")
	if ref == "" {
		ref = r.URL.Query().Get("name")
	}
	if ref == "" {
		s.writeGraphFailure(w, r, http.StatusBadRequest, "entity id or name is required")
		return
	}
	entity, err := s.findEntity(r.Context(), ref)
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusNotFound, err.Error())
		return
	}
	relations, err := s.deps.Graph.AllRelations(r.Context())
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "listing relations failed: "+err.Error())
		return
	}
	touched := map[string]struct{}{entity.ID: {}}
	for _, rel := range relations {
		if rel.Src == entity.ID {
			touched[rel.Dst] = struct{}{}
		}
		if rel.Dst == entity.ID {
			touched[rel.Src] = struct{}{}
		}
	}
	if err := s.deps.Graph.DeleteEntity(r.Context(), entity.ID); err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "delete entity failed: "+err.Error())
		return
	}
	if err := s.refreshGraph(r.Context(), mapKeys(touched)); err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "refreshing graph failed: "+err.Error())
		return
	}
	s.writeGraphMutationSuccess(w, r, map[string]string{"status": "ok", "id": entity.ID})
}

func (s *Server) handleGraphRelationUpsert(w http.ResponseWriter, r *http.Request) {
	if s.deps.Graph == nil {
		s.writeGraphFailure(w, r, http.StatusServiceUnavailable, "knowledge graph is not configured")
		return
	}
	payload, err := parseGraphRelationPayload(r)
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if payload.Src == "" {
		s.writeGraphFailure(w, r, http.StatusBadRequest, "relation source is required")
		return
	}
	if payload.Dst == "" {
		s.writeGraphFailure(w, r, http.StatusBadRequest, "relation target is required")
		return
	}
	if payload.Type == "" {
		s.writeGraphFailure(w, r, http.StatusBadRequest, "relation type is required")
		return
	}
	src, err := s.findEntity(r.Context(), payload.Src)
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusBadRequest, "relation source: "+err.Error())
		return
	}
	dst, err := s.findEntity(r.Context(), payload.Dst)
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusBadRequest, "relation target: "+err.Error())
		return
	}
	rel, err := payload.relation(src.ID, dst.ID)
	if err != nil {
		s.writeGraphFailure(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if rel.Provenance == provenanceUserAccepted {
		if rel.ValidFrom == nil {
			now := s.deps.Now()
			rel.ValidFrom = &now
		}
		rel.Reopen = true
	}
	if err := s.deps.Graph.PutRelation(r.Context(), rel); err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "save relation failed: "+err.Error())
		return
	}
	if err := s.refreshGraph(r.Context(), []string{src.ID, dst.ID}); err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "refreshing graph failed: "+err.Error())
		return
	}
	if isHtmx(r) && rel.Provenance == provenanceUserAccepted {
		s.render(w, "node-view", http.StatusOK, page{Title: src.Name, Data: s.buildGraphNodePage(r.Context(), src, parseGraphNodeParams(r))})
		return
	}
	s.writeGraphMutationSuccess(w, r, rel)
}

func (p graphRelationPayload) relation(srcID, dstID string) (graphstore.Relation, error) {
	id := p.ID
	if id == "" {
		id = graph.RelationID(srcID, dstID, p.Type)
	}
	chunks, err := chunksWithDefault(p.SourceChunks, "manual:"+id)
	if err != nil {
		return graphstore.Relation{}, err
	}
	weight := p.Weight
	if weight <= 0 {
		weight = 1
	}
	var validFrom *time.Time
	if p.ValidFrom != "" {
		parsed, err := time.Parse(time.RFC3339Nano, p.ValidFrom)
		if err != nil {
			return graphstore.Relation{}, fmt.Errorf("invalid valid_from: %w", err)
		}
		validFrom = &parsed
	}
	return graphstore.Relation{
		ID: id, Src: srcID, Dst: dstID, Type: p.Type,
		Description: p.Description, Weight: weight, Confidence: p.Confidence,
		Provenance: p.Provenance, ValidFrom: validFrom, SourceChunks: chunks,
	}, nil
}

func (s *Server) handleGraphRelationDelete(w http.ResponseWriter, r *http.Request) {
	if s.deps.Graph == nil {
		s.writeGraphFailure(w, r, http.StatusServiceUnavailable, "knowledge graph is not configured")
		return
	}
	id := r.URL.Query().Get("id")
	var srcID, dstID string
	if id == "" {
		payload, err := parseGraphRelationPayload(r)
		if err != nil {
			s.writeGraphFailure(w, r, http.StatusBadRequest, err.Error())
			return
		}
		if payload.Src == "" || payload.Dst == "" || payload.Type == "" {
			s.writeGraphFailure(w, r, http.StatusBadRequest, "relation id or src/dst/type is required")
			return
		}
		src, err := s.findEntity(r.Context(), payload.Src)
		if err != nil {
			s.writeGraphFailure(w, r, http.StatusBadRequest, "relation source: "+err.Error())
			return
		}
		dst, err := s.findEntity(r.Context(), payload.Dst)
		if err != nil {
			s.writeGraphFailure(w, r, http.StatusBadRequest, "relation target: "+err.Error())
			return
		}
		id = graph.RelationID(src.ID, dst.ID, payload.Type)
		srcID, dstID = src.ID, dst.ID
	}

	if srcID == "" {
		relations, err := s.deps.Graph.AllRelations(r.Context())
		if err != nil {
			s.writeGraphFailure(w, r, http.StatusInternalServerError, "listing relations failed: "+err.Error())
			return
		}
		found := false
		for _, rel := range relations {
			if rel.ID == id {
				srcID, dstID = rel.Src, rel.Dst
				found = true
				break
			}
		}
		if !found {
			s.writeGraphFailure(w, r, http.StatusNotFound, "relation not found: "+id)
			return
		}
	}

	if err := s.deps.Graph.DeleteRelation(r.Context(), id); err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "delete relation failed: "+err.Error())
		return
	}
	if err := s.refreshGraph(r.Context(), []string{srcID, dstID}); err != nil {
		s.writeGraphFailure(w, r, http.StatusInternalServerError, "refreshing graph failed: "+err.Error())
		return
	}
	s.writeGraphMutationSuccess(w, r, map[string]string{"status": "ok", "id": id})
}

func (s *Server) findEntity(ctx context.Context, ref string) (graphstore.Entity, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return graphstore.Entity{}, errors.New("entity reference is required")
	}
	entities, err := s.deps.Graph.AllEntities(ctx)
	if err != nil {
		return graphstore.Entity{}, err
	}
	for _, e := range entities {
		if e.ID == ref || e.Name == ref {
			return e, nil
		}
	}
	return graphstore.Entity{}, fmt.Errorf("entity %q not found", ref)
}

func splitSourceChunks(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' ' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func chunksWithDefault(chunks []string, fallback string) ([]string, error) {
	if len(chunks) == 0 {
		return []string{fallback}, nil
	}
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			return nil, errors.New("source_chunks contains an empty entry")
		}
	}
	return chunks, nil
}

func mapKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Server) refreshGraph(ctx context.Context, entityIDs []string) error {
	if s.deps.GraphUpdater != nil {
		if _, err := s.deps.GraphUpdater.RecomputeCommunities(ctx, entityIDs); err != nil {
			return err
		}
	}
	s.refreshBM25(ctx)
	return nil
}

func (s *Server) writeGraphMutationSuccess(w http.ResponseWriter, r *http.Request, payload any) {
	if isHtmx(r) {
		data, err := s.graphView(r.Context())
		if err != nil {
			s.writeGraphFailure(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		s.render(w, "graph-content", http.StatusOK, page{Title: "Knowledge graph", Data: data})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) writeGraphFailure(w http.ResponseWriter, r *http.Request, status int, message string) {
	if isHtmx(r) {
		s.render(w, "graph-error", status, page{Title: message})
		return
	}
	http.Error(w, message, status)
}
