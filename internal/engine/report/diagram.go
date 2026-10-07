package report

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
)

const (
	defaultDiagramHops     = 1
	defaultDiagramMaxNodes = 24
	maxDiagramHops         = 3
	maxDiagramNodesCap     = 60
	diagramLabelRunes      = 48
	diagramDescRunes       = 140
	diagramMinNodes        = 2
)

const diagramSystemPrompt = `You turn a slice of a knowledge graph into an architecture diagram plan. ` +
	`You receive entities with short ids (e1, e2, ...) and relations between them. ` +
	`Pick the entities that matter for understanding the structure, give each a short label ` +
	`(at most 4 words, same language as the entity name) and put related entities into a named group ` +
	`(a layer or component area, at most 3 words). Pick relations only from the given list and give each ` +
	`a short verb label (at most 3 words). Use ONLY the given ids and relations, never invent any. ` +
	`Reply with JSON only, no markdown fences, in the form: ` +
	`{"nodes":[{"id":"e1","label":"...","group":"..."}],"edges":[{"from":"e1","to":"e2","label":"..."}]}`

type DiagramGraph interface {
	MatchEntities(ctx context.Context, names []string, at ...time.Time) ([]graphstore.Entity, error)
	Neighbors(ctx context.Context, entityID string, hops int, at ...time.Time) ([]graphstore.Entity, []graphstore.Relation, error)
	AllEntities(ctx context.Context) ([]graphstore.Entity, error)
	AllRelations(ctx context.Context) ([]graphstore.Relation, error)
}

type DiagramOptions struct {
	Hops     int
	MaxNodes int
}

type DiagramResult struct {
	Mermaid  string
	Focus    string
	Nodes    int
	Edges    int
	Dropped  []string
	Fallback bool
	Reason   string
}

type diagramPlan struct {
	Nodes []diagramPlanNode `json:"nodes"`
	Edges []diagramPlanEdge `json:"edges"`
}

type diagramPlanNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
}

type diagramPlanEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

type diagramNode struct {
	entity graphstore.Entity
	label  string
	group  string
}

type diagramEdge struct {
	from, to string
	label    string
}

func Diagram(ctx context.Context, g DiagramGraph, chat ChatClient, model, focus string, opt DiagramOptions) (DiagramResult, error) {
	opt = normalizeDiagramOptions(opt)
	res := DiagramResult{Focus: strings.TrimSpace(focus)}
	if g == nil {
		res.Fallback = true
		res.Reason = "knowledge graph is not configured"
		return res, nil
	}

	entities, relations, err := selectDiagramSlice(ctx, g, res.Focus, opt)
	if err != nil {
		return res, err
	}
	if len(entities) == 0 {
		res.Fallback = true
		if res.Focus != "" {
			res.Reason = fmt.Sprintf("no entity matches %q", res.Focus)
		} else {
			res.Reason = "knowledge graph is empty"
		}
		return res, nil
	}
	if len(relations) == 0 {
		res.Fallback = true
		res.Reason = "no relations in the selected slice"
		nodes := fallbackDiagramNodes(entities)
		res.Mermaid, res.Nodes = renderDiagram(nodes, nil), len(nodes)
		return res, nil
	}

	aliases, byAlias := diagramAliases(entities)
	nodes, edges, dropped, reason := planDiagram(ctx, chat, model, res.Focus, entities, relations, aliases, byAlias)
	res.Dropped = dropped
	if reason != "" {
		res.Fallback = true
		res.Reason = reason
		nodes = fallbackDiagramNodes(entities)
		edges = fallbackDiagramEdges(relations, nodes)
	}
	res.Mermaid = renderDiagram(nodes, edges)
	res.Nodes, res.Edges = len(nodes), len(edges)
	return res, nil
}

func normalizeDiagramOptions(opt DiagramOptions) DiagramOptions {
	if opt.Hops <= 0 {
		opt.Hops = defaultDiagramHops
	}
	if opt.Hops > maxDiagramHops {
		opt.Hops = maxDiagramHops
	}
	if opt.MaxNodes <= 0 {
		opt.MaxNodes = defaultDiagramMaxNodes
	}
	if opt.MaxNodes > maxDiagramNodesCap {
		opt.MaxNodes = maxDiagramNodesCap
	}
	return opt
}

func selectDiagramSlice(ctx context.Context, g DiagramGraph, focus string, opt DiagramOptions) ([]graphstore.Entity, []graphstore.Relation, error) {
	if focus == "" {
		all, err := g.AllEntities(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("report: diagram: load entities: %w", err)
		}
		allRel, err := g.AllRelations(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("report: diagram: load relations: %w", err)
		}
		entities := topByDegree(all, opt.MaxNodes, "")
		return entities, relationsAmong(allRel, entities), nil
	}

	center, ok, err := resolveDiagramFocus(ctx, g, focus)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, nil
	}
	neighbors, rels, err := g.Neighbors(ctx, center.ID, opt.Hops)
	if err != nil {
		return nil, nil, fmt.Errorf("report: diagram: neighbors: %w", err)
	}
	pool := append([]graphstore.Entity{center}, neighbors...)
	entities := topByDegree(pool, opt.MaxNodes, center.ID)
	return entities, relationsAmong(rels, entities), nil
}

func resolveDiagramFocus(ctx context.Context, g DiagramGraph, focus string) (graphstore.Entity, bool, error) {
	matched, err := g.MatchEntities(ctx, []string{focus})
	if err != nil {
		return graphstore.Entity{}, false, fmt.Errorf("report: diagram: match entity: %w", err)
	}
	if len(matched) > 0 {
		return bestByDegree(matched), true, nil
	}
	all, err := g.AllEntities(ctx)
	if err != nil {
		return graphstore.Entity{}, false, fmt.Errorf("report: diagram: load entities: %w", err)
	}
	needle := strings.ToLower(focus)
	var exactID, contains []graphstore.Entity
	for _, e := range all {
		switch {
		case e.ID == focus:
			exactID = append(exactID, e)
		case strings.Contains(strings.ToLower(e.Name), needle):
			contains = append(contains, e)
		}
	}
	if len(exactID) > 0 {
		return exactID[0], true, nil
	}
	if len(contains) > 0 {
		return bestByDegree(contains), true, nil
	}
	return graphstore.Entity{}, false, nil
}

func bestByDegree(entities []graphstore.Entity) graphstore.Entity {
	best := entities[0]
	for _, e := range entities[1:] {
		if e.Degree > best.Degree || (e.Degree == best.Degree && e.Name < best.Name) {
			best = e
		}
	}
	return best
}

func topByDegree(entities []graphstore.Entity, limit int, keepID string) []graphstore.Entity {
	seen := make(map[string]bool, len(entities))
	uniq := make([]graphstore.Entity, 0, len(entities))
	for _, e := range entities {
		if e.ID == "" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		uniq = append(uniq, e)
	}
	sort.SliceStable(uniq, func(i, j int) bool {
		if keepID != "" {
			if (uniq[i].ID == keepID) != (uniq[j].ID == keepID) {
				return uniq[i].ID == keepID
			}
		}
		if uniq[i].Degree != uniq[j].Degree {
			return uniq[i].Degree > uniq[j].Degree
		}
		return uniq[i].Name < uniq[j].Name
	})
	if len(uniq) > limit {
		uniq = uniq[:limit]
	}
	return uniq
}

func relationsAmong(relations []graphstore.Relation, entities []graphstore.Entity) []graphstore.Relation {
	in := make(map[string]bool, len(entities))
	for _, e := range entities {
		in[e.ID] = true
	}
	var out []graphstore.Relation
	seen := make(map[string]bool)
	for _, r := range relations {
		if r.ExpiredAt != nil || r.Src == r.Dst || !in[r.Src] || !in[r.Dst] {
			continue
		}
		key := r.Src + "\x00" + r.Dst + "\x00" + r.Type
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		if out[i].Dst != out[j].Dst {
			return out[i].Dst < out[j].Dst
		}
		return out[i].Type < out[j].Type
	})
	return out
}

func diagramAliases(entities []graphstore.Entity) (map[string]string, map[string]graphstore.Entity) {
	aliases := make(map[string]string, len(entities))
	byAlias := make(map[string]graphstore.Entity, len(entities))
	for i, e := range entities {
		alias := fmt.Sprintf("e%d", i+1)
		aliases[e.ID] = alias
		byAlias[alias] = e
	}
	return aliases, byAlias
}

func planDiagram(ctx context.Context, chat ChatClient, model, focus string, entities []graphstore.Entity, relations []graphstore.Relation, aliases map[string]string, byAlias map[string]graphstore.Entity) ([]diagramNode, []diagramEdge, []string, string) {
	if chat == nil {
		return nil, nil, nil, "diagram planning unavailable: no chat client configured"
	}
	resp, err := chat.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: diagramSystemPrompt},
			{Role: "user", Content: buildDiagramPrompt(focus, entities, relations, aliases)},
		},
	})
	if err != nil {
		return nil, nil, nil, "diagram planning failed: " + err.Error()
	}
	plan, ok := parseDiagramPlan(resp.Content)
	if !ok {
		return nil, nil, nil, "diagram planning returned unparseable reply"
	}
	nodes, edges, dropped := validateDiagramPlan(plan, relations, aliases, byAlias)
	if len(nodes) < diagramMinNodes || len(edges) == 0 {
		return nil, nil, dropped, "diagram plan had too few valid nodes or edges"
	}
	return nodes, edges, dropped, ""
}

func buildDiagramPrompt(focus string, entities []graphstore.Entity, relations []graphstore.Relation, aliases map[string]string) string {
	var b strings.Builder
	if focus != "" {
		fmt.Fprintf(&b, "Focus: %s\n\n", focus)
	}
	b.WriteString("Entities (id | name | type | description):\n")
	for _, e := range entities {
		fmt.Fprintf(&b, "%s | %s | %s | %s\n", aliases[e.ID], oneLine(e.Name, diagramLabelRunes), oneLine(e.Type, 24), oneLine(e.Description, diagramDescRunes))
	}
	b.WriteString("\nRelations (from -> to [type]: description):\n")
	for _, r := range relations {
		fmt.Fprintf(&b, "%s -> %s [%s]: %s\n", aliases[r.Src], aliases[r.Dst], oneLine(r.Type, 32), oneLine(r.Description, diagramDescRunes))
	}
	return b.String()
}

var thinkBlock = regexp.MustCompile(`(?s)<think>.*?</think>`)

func parseDiagramPlan(content string) (diagramPlan, bool) {
	content = thinkBlock.ReplaceAllString(content, "")
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return diagramPlan{}, false
	}
	var plan diagramPlan
	if err := json.Unmarshal([]byte(content[start:end+1]), &plan); err != nil {
		return diagramPlan{}, false
	}
	return plan, true
}

func validateDiagramPlan(plan diagramPlan, relations []graphstore.Relation, aliases map[string]string, byAlias map[string]graphstore.Entity) ([]diagramNode, []diagramEdge, []string) {
	var dropped []string
	nodeByID := make(map[string]diagramNode)
	var nodes []diagramNode
	for _, n := range plan.Nodes {
		e, ok := byAlias[strings.TrimSpace(n.ID)]
		if !ok {
			dropped = append(dropped, "node "+oneLine(n.ID, 32)+": unknown id")
			continue
		}
		if _, dup := nodeByID[e.ID]; dup {
			continue
		}
		label := oneLine(n.Label, diagramLabelRunes)
		if label == "" {
			label = oneLine(e.Name, diagramLabelRunes)
		}
		node := diagramNode{entity: e, label: label, group: oneLine(n.Group, 32)}
		nodeByID[e.ID] = node
		nodes = append(nodes, node)
	}

	type pair struct{ a, b string }
	known := make(map[pair]graphstore.Relation, len(relations))
	for _, r := range relations {
		if _, ok := known[pair{r.Src, r.Dst}]; !ok {
			known[pair{r.Src, r.Dst}] = r
		}
	}

	var edges []diagramEdge
	seen := make(map[pair]bool)
	for _, e := range plan.Edges {
		from, okFrom := byAlias[strings.TrimSpace(e.From)]
		to, okTo := byAlias[strings.TrimSpace(e.To)]
		if !okFrom || !okTo {
			dropped = append(dropped, "edge "+oneLine(e.From+"->"+e.To, 48)+": unknown id")
			continue
		}
		rel, ok := known[pair{from.ID, to.ID}]
		if !ok {
			rel, ok = known[pair{to.ID, from.ID}]
		}
		if !ok {
			dropped = append(dropped, "edge "+oneLine(e.From+"->"+e.To, 48)+": no such relation")
			continue
		}
		if _, ok := nodeByID[rel.Src]; !ok {
			dropped = append(dropped, "edge "+aliases[rel.Src]+"->"+aliases[rel.Dst]+": endpoint not selected")
			continue
		}
		if _, ok := nodeByID[rel.Dst]; !ok {
			dropped = append(dropped, "edge "+aliases[rel.Src]+"->"+aliases[rel.Dst]+": endpoint not selected")
			continue
		}
		key := pair{rel.Src, rel.Dst}
		if seen[key] {
			continue
		}
		seen[key] = true
		label := oneLine(e.Label, 32)
		if label == "" {
			label = oneLine(rel.Type, 32)
		}
		edges = append(edges, diagramEdge{from: rel.Src, to: rel.Dst, label: label})
	}
	return nodes, edges, dropped
}

func fallbackDiagramNodes(entities []graphstore.Entity) []diagramNode {
	nodes := make([]diagramNode, 0, len(entities))
	for _, e := range entities {
		nodes = append(nodes, diagramNode{entity: e, label: oneLine(e.Name, diagramLabelRunes), group: oneLine(e.Type, 32)})
	}
	return nodes
}

func fallbackDiagramEdges(relations []graphstore.Relation, nodes []diagramNode) []diagramEdge {
	in := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		in[n.entity.ID] = true
	}
	var edges []diagramEdge
	for _, r := range relations {
		if in[r.Src] && in[r.Dst] {
			edges = append(edges, diagramEdge{from: r.Src, to: r.Dst, label: oneLine(r.Type, 32)})
		}
	}
	return edges
}

func renderDiagram(nodes []diagramNode, edges []diagramEdge) string {
	ids := make(map[string]string, len(nodes))
	for i, n := range nodes {
		ids[n.entity.ID] = fmt.Sprintf("n%d", i+1)
	}

	groupSize := make(map[string]int)
	for _, n := range nodes {
		if n.group != "" {
			groupSize[strings.ToLower(n.group)]++
		}
	}

	var b strings.Builder
	b.WriteString("flowchart LR\n")
	rendered := make(map[string]bool, len(nodes))
	groupIdx := 0
	for _, n := range nodes {
		key := strings.ToLower(n.group)
		if n.group == "" || groupSize[key] < 2 || rendered["g:"+key] {
			continue
		}
		rendered["g:"+key] = true
		groupIdx++
		fmt.Fprintf(&b, "  subgraph g%d[\"%s\"]\n", groupIdx, mermaidText(n.group))
		for _, m := range nodes {
			if strings.ToLower(m.group) == key {
				fmt.Fprintf(&b, "    %s[\"%s\"]\n", ids[m.entity.ID], mermaidText(m.label))
				rendered[m.entity.ID] = true
			}
		}
		b.WriteString("  end\n")
	}
	for _, n := range nodes {
		if !rendered[n.entity.ID] {
			fmt.Fprintf(&b, "  %s[\"%s\"]\n", ids[n.entity.ID], mermaidText(n.label))
		}
	}
	for _, e := range edges {
		from, to := ids[e.from], ids[e.to]
		if from == "" || to == "" {
			continue
		}
		if e.label == "" {
			fmt.Fprintf(&b, "  %s --> %s\n", from, to)
			continue
		}
		fmt.Fprintf(&b, "  %s -->|\"%s\"| %s\n", from, mermaidText(e.label), to)
	}
	return b.String()
}

func mermaidText(s string) string {
	s = strings.ReplaceAll(s, `"`, "#quot;")
	s = strings.ReplaceAll(s, "<", "#lt;")
	s = strings.ReplaceAll(s, ">", "#gt;")
	return s
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return s
}
