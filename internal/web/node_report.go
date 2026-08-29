package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/engine/report"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

const (
	nodeReportDefaultPerType = 10
	nodeReportMaxPerType     = 100
)

type nodeReportGroup struct {
	Name  string
	Count int
}

type nodeReportDrop struct {
	Name   string
	Reason string
}

type nodeReportContext struct {
	Entity        graphstore.Entity
	Chunks        []vector.ScoredChunk
	TokenEstimate int
	Groups        []nodeReportGroup
	Dropped       []nodeReportDrop
	Warnings      []string
}

type nodeReportView struct {
	NodeID   string
	NodeName string
	Report   template.HTML
	Warnings []string
}

func estimateTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return (len([]rune(text)) + 3) / 4
}

func authorityRank(authority string) int {
	switch strings.ToLower(strings.TrimSpace(authority)) {
	case "approved":
		return 0
	case "notes":
		return 1
	default:
		return 2
	}
}

type nodeNeighborCandidate struct {
	neighbor      graphstore.Entity
	authorityRank int
	age           time.Duration
}

func relationReportScore(sourceChunks []string, chunks map[string]vector.Chunk, now time.Time) (int, time.Duration) {
	rank := 2
	age := time.Duration(1<<63 - 1)
	for _, chunkID := range sourceChunks {
		chunk, ok := chunks[chunkID]
		if !ok {
			continue
		}
		if rank == 2 {
			if authority := strings.TrimSpace(chunk.Metadata["visibility"]); authority != "" {
				rank = authorityRank(authority)
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
	return rank, age
}

func (s *Server) nodeReportLimit(r *http.Request) int {
	limit := nodeReportDefaultPerType
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > nodeReportMaxPerType {
		limit = nodeReportMaxPerType
	}
	return limit
}

func (s *Server) buildNodeReportContext(ctx context.Context, nodeID string, limitPerType int) nodeReportContext {
	if limitPerType <= 0 {
		limitPerType = nodeReportDefaultPerType
	}
	if limitPerType > nodeReportMaxPerType {
		limitPerType = nodeReportMaxPerType
	}
	res := nodeReportContext{}
	if s.deps.Graph == nil {
		res.Warnings = append(res.Warnings, "knowledge graph is not configured")
		return res
	}

	entity, err := s.findEntity(ctx, nodeID)
	if err != nil {
		res.Warnings = append(res.Warnings, "loading entity failed: "+err.Error())
		return res
	}
	res.Entity = entity

	chunks := s.chunkIndex(ctx)
	now := s.deps.Now()

	var neighbors []graphstore.Entity
	var relations []graphstore.Relation
	neighbors, relations, err = s.deps.Graph.Neighbors(ctx, entity.ID, 1)
	if err != nil {
		res.Warnings = append(res.Warnings, "loading neighbors failed: "+err.Error())
	}
	neighborsByID := make(map[string]graphstore.Entity, len(neighbors))
	for _, neighbor := range neighbors {
		neighborsByID[neighbor.ID] = neighbor
	}

	seen := make(map[string]bool)

	if path := s.entityDocPath(ctx, entity.SourceChunks); path != "" {
		if doc, err := s.readDocRaw(path); err == nil {
			fileName := filepath.Base(path)
			if fileName == "" || fileName == "." || fileName == "/" {
				fileName = filepath.ToSlash(path)
			}
			res.Chunks = append(res.Chunks, vector.ScoredChunk{Chunk: vector.Chunk{
				ID:       "node-doc:" + entity.ID,
				FileName: fileName,
				FilePath: filepath.ToSlash(path),
				Text:     doc.Body,
			}})
			res.Groups = append(res.Groups, nodeReportGroup{Name: "document " + filepath.ToSlash(path), Count: 1})
			for _, chunkID := range entity.SourceChunks {
				if chunk, ok := chunks[chunkID]; ok && chunk.FilePath == path {
					seen[chunkID] = true
				}
			}
		}
	}
	if len(res.Chunks) == 0 {
		for _, chunkID := range entity.SourceChunks {
			if chunk, ok := chunks[chunkID]; ok && !seen[chunkID] {
				seen[chunkID] = true
				res.Chunks = append(res.Chunks, vector.ScoredChunk{Chunk: chunk})
			}
		}
		if len(res.Chunks) > 0 {
			res.Groups = append(res.Groups, nodeReportGroup{Name: "node document chunks", Count: len(res.Chunks)})
		}
	}

	byType := make(map[string][]nodeNeighborCandidate)
	for _, relation := range relations {
		if relation.Src != entity.ID && relation.Dst != entity.ID {
			continue
		}
		neighborID := relation.Src
		if neighborID == entity.ID {
			neighborID = relation.Dst
		}
		if neighborID == entity.ID {
			continue
		}
		neighbor, ok := neighborsByID[neighborID]
		if !ok {
			continue
		}
		rank, age := relationReportScore(relation.SourceChunks, chunks, now)
		byType[relation.Type] = append(byType[relation.Type], nodeNeighborCandidate{
			neighbor:      neighbor,
			authorityRank: rank,
			age:           age,
		})
	}

	relationTypes := make([]string, 0, len(byType))
	for relationType := range byType {
		relationTypes = append(relationTypes, relationType)
	}
	sort.Strings(relationTypes)

	for _, relationType := range relationTypes {
		candidates := byType[relationType]
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].authorityRank != candidates[j].authorityRank {
				return candidates[i].authorityRank < candidates[j].authorityRank
			}
			if candidates[i].age != candidates[j].age {
				return candidates[i].age < candidates[j].age
			}
			return candidates[i].neighbor.ID < candidates[j].neighbor.ID
		})

		includedCount := len(candidates)
		if includedCount > limitPerType {
			includedCount = limitPerType
		}
		for _, candidate := range candidates[:includedCount] {
			addNeighborChunks(candidate.neighbor, chunks, &res.Chunks, seen)
		}
		if includedCount > 0 {
			res.Groups = append(res.Groups, nodeReportGroup{Name: relationType + " neighbours", Count: includedCount})
		}
		for _, candidate := range candidates[includedCount:] {
			name := candidate.neighbor.Name
			if name == "" {
				name = candidate.neighbor.ID
			}
			res.Dropped = append(res.Dropped, nodeReportDrop{
				Name:   name,
				Reason: fmt.Sprintf("лимит %d на тип %q", limitPerType, relationType),
			})
		}
	}

	if communities, err := s.deps.Graph.CommunitiesFor(ctx, []string{entity.ID}); err != nil {
		res.Warnings = append(res.Warnings, "loading communities failed: "+err.Error())
	} else {
		count := 0
		for _, community := range communities {
			if strings.TrimSpace(community.Summary) == "" {
				continue
			}
			title := community.Title
			if strings.TrimSpace(title) == "" {
				title = community.ID
			}
			res.Chunks = append(res.Chunks, vector.ScoredChunk{Chunk: vector.Chunk{
				ID:       "community:" + community.ID,
				FileName: "community " + title,
				Text:     community.Summary,
			}})
			count++
		}
		if count > 0 {
			res.Groups = append(res.Groups, nodeReportGroup{Name: "community summaries", Count: count})
		}
	}

	res.TokenEstimate = 0
	for _, chunk := range res.Chunks {
		res.TokenEstimate += estimateTokens(chunk.Text)
	}
	return res
}

func addNeighborChunks(neighbor graphstore.Entity, chunks map[string]vector.Chunk, out *[]vector.ScoredChunk, seen map[string]bool) int {
	count := 0
	for _, chunkID := range neighbor.SourceChunks {
		if seen[chunkID] {
			continue
		}
		chunk, ok := chunks[chunkID]
		if !ok {
			continue
		}
		seen[chunkID] = true
		*out = append(*out, vector.ScoredChunk{Chunk: chunk})
		count++
	}
	return count
}

func nodeContextMarkdown(c nodeReportContext) string {
	var b strings.Builder
	b.WriteString("\n\n## Что вошло в контекст\n")
	if len(c.Groups) == 0 {
		b.WriteString("- ничего\n")
	} else {
		for _, group := range c.Groups {
			fmt.Fprintf(&b, "- %s: %d\n", group.Name, group.Count)
		}
	}
	if len(c.Dropped) > 0 {
		b.WriteString("\n## Что отброшено\n")
		for _, dropped := range c.Dropped {
			fmt.Fprintf(&b, "- %s: %s\n", dropped.Name, dropped.Reason)
		}
	}
	fmt.Fprintf(&b, "\n*Оценка контекста: %d токенов.*\n", c.TokenEstimate)
	return b.String()
}

func (s *Server) handleNodeReportEstimate(w http.ResponseWriter, r *http.Request) {
	nodeID := strings.TrimSpace(r.URL.Query().Get("id"))
	if nodeID == "" {
		nodeID = strings.TrimSpace(r.URL.Query().Get("node"))
	}
	data := s.buildNodeReportContext(r.Context(), nodeID, s.nodeReportLimit(r))
	s.render(w, "node-report-estimate", http.StatusOK, page{Title: "Node report", Data: data})
}

func (s *Server) handleNodeReport(w http.ResponseWriter, r *http.Request, nodeID, query string) {
	ctx := r.Context()
	data := s.buildNodeReportContext(ctx, nodeID, nodeReportDefaultPerType)
	if query == "" {
		if data.Entity.Name != "" {
			query = "Отчёт о ноде: " + data.Entity.Name
		} else {
			query = "Отчёт о ноде"
		}
	}

	synthesis, fallback, _ := report.SynthesizeResult(ctx, s.deps.Chat, s.deps.LLMModel, query, data.Chunks)
	if fallback {
		data.Warnings = append(data.Warnings, "синтез недоступен — показан список источников")
	}

	view := nodeReportView{
		NodeID:   data.Entity.ID,
		NodeName: data.Entity.Name,
		Report:   renderMarkdown(synthesis + nodeContextMarkdown(data)),
		Warnings: data.Warnings,
	}

	if isHtmx(r) {
		s.render(w, "node-report-result", http.StatusOK, page{Title: "Node report", Data: view})
		return
	}
	s.render(w, "page-reports", http.StatusOK, page{
		Title:  "Reports",
		Alerts: alertsFromWarnings(data.Warnings),
		Data: reportsData{
			Mode:   "search",
			Query:  query,
			Report: view.Report,
		},
	})
}

func alertsFromWarnings(warnings []string) []Alert {
	out := make([]Alert, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, Alert{Kind: "warning", Message: warning})
	}
	return out
}
