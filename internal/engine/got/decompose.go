package got

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/alterfo/kb/internal/llm"
)

const decomposeSystemPrompt = `You break a user question into 2-5 focused, self-contained sub-questions ` +
	`that together cover what is needed to answer it fully. Some sub-questions may depend on the answers ` +
	`of earlier ones. Respond with a JSON array of objects only, each of the form ` +
	`{"subquestion": "...", "depends_on": [0, 2]} where "depends_on" lists zero-based indices of the ` +
	`sub-questions this one depends on (omit it or use [] when there is no dependency). ` +
	`No prose, no markdown fences.`

const findGapsSystemPrompt = `Given the original question and a draft answer built from sub-answers, list what ` +
	`important information is still missing, as follow-up sub-questions. Respond with a JSON array of objects only, ` +
	`each of the form {"subquestion": "...", "reported_by": 0} where "reported_by" is the zero-based index of the ` +
	`sub-answer whose weakness exposed the gap (omit it or use null when the gap applies to the whole draft). ` +
	`Empty array if nothing is missing. No prose, no markdown fences.`

// decompose asks the LLM to split query into sub-questions with their
// dependencies. Fails open to []subgoalSpec{{Query: query}} on any error,
// empty response or nil chat.
func (o *Orchestrator) decompose(ctx context.Context, query string) []subgoalSpec {
	if o.cfg.Chat == nil || strings.TrimSpace(query) == "" {
		return []subgoalSpec{{Query: query}}
	}

	resp, ok := o.chat(ctx, llm.ChatRequest{
		Model: o.cfg.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: decomposeSystemPrompt},
			{Role: "user", Content: query},
		},
	})
	if !ok {
		return []subgoalSpec{{Query: query}}
	}

	specs := parseSubgoalSpecs(resp.Content)
	if len(specs) == 0 {
		return []subgoalSpec{{Query: query}}
	}
	if len(specs) > o.cfg.MaxSubgoals {
		specs = specs[:o.cfg.MaxSubgoals]
	}
	return specs
}

// findGaps asks the LLM what is still missing from draft, recording which
// subgoal reported each gap. Fails open to no gaps (nil), which means Run
// will not attempt a refine pass.
func (o *Orchestrator) findGaps(ctx context.Context, query, draft string) []gapSpec {
	if o.cfg.Chat == nil {
		return nil
	}

	resp, ok := o.chat(ctx, llm.ChatRequest{
		Model: o.cfg.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: findGapsSystemPrompt},
			{Role: "user", Content: "Question: " + query + "\n\nDraft answer:\n" + draft},
		},
	})
	if !ok {
		return nil
	}
	return parseGapSpecs(resp.Content)
}

// gapSpec is one follow-up sub-question surfaced by find_gaps together with
// the zero-based index of the subgoal that reported it. ReportedBy -1 means
// the gap is attributed to the whole draft rather than one subgoal.
type gapSpec struct {
	Query      string
	ReportedBy int
}

// gapItem is one element of the new find_gaps JSON format.
type gapItem struct {
	Subquestion string `json:"subquestion"`
	ReportedBy  int    `json:"reported_by"`
}

func (g *gapItem) UnmarshalJSON(data []byte) error {
	var aux struct {
		Subquestion string          `json:"subquestion"`
		ReportedBy  json.RawMessage `json:"reported_by"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	g.Subquestion = strings.TrimSpace(aux.Subquestion)
	g.ReportedBy = -1
	if len(aux.ReportedBy) == 0 || string(aux.ReportedBy) == "null" {
		return nil
	}
	if s, ok := rawIndex(aux.ReportedBy); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			g.ReportedBy = n
		}
	}
	return nil
}

// parseGapSpecs decodes the find_gaps response. It accepts the new object
// format with "reported_by" and falls back to the legacy flat string array.
// Garbage yields nil, which the caller turns into no refine pass.
func parseGapSpecs(content string) []gapSpec {
	content = stripCodeFence(content)

	var items []gapItem
	if err := json.Unmarshal([]byte(content), &items); err == nil && len(items) > 0 {
		out := make([]gapSpec, 0, len(items))
		for _, it := range items {
			if strings.TrimSpace(it.Subquestion) == "" {
				continue
			}
			out = append(out, gapSpec{Query: strings.TrimSpace(it.Subquestion), ReportedBy: it.ReportedBy})
		}
		return out
	}

	var list []string
	if err := json.Unmarshal([]byte(content), &list); err != nil {
		return nil
	}
	out := make([]gapSpec, 0, len(list))
	for _, s := range cleanStringList(list) {
		out = append(out, gapSpec{Query: s, ReportedBy: -1})
	}
	return out
}

// parseSubgoalSpecs decodes the decompose response. It accepts the new
// object format with "depends_on" indices and falls back to the legacy flat
// string array. Garbage yields nil, which the caller turns into a single
// fail-open subgoal.
func parseSubgoalSpecs(content string) []subgoalSpec {
	content = stripCodeFence(content)

	var items []decomposeItem
	if err := json.Unmarshal([]byte(content), &items); err == nil && len(items) > 0 {
		return cleanSubgoalItems(items)
	}

	var list []string
	if err := json.Unmarshal([]byte(content), &list); err == nil {
		out := make([]subgoalSpec, 0, len(list))
		for _, s := range cleanStringList(list) {
			out = append(out, subgoalSpec{Query: s})
		}
		return out
	}
	return nil
}

// decomposeItem is one element of the new decompose JSON format.
type decomposeItem struct {
	Subquestion string   `json:"subquestion"`
	DependsOn   []string `json:"depends_on"`
}

func (d *decomposeItem) UnmarshalJSON(data []byte) error {
	var aux struct {
		Subquestion string            `json:"subquestion"`
		DependsOn   []json.RawMessage `json:"depends_on"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	d.Subquestion = strings.TrimSpace(aux.Subquestion)
	var deps []string
	for _, raw := range aux.DependsOn {
		if s, ok := rawIndex(raw); ok {
			deps = append(deps, s)
		}
	}
	d.DependsOn = deps
	return nil
}

// rawIndex decodes a depends_on element, accepting both numeric and string
// indices. Non-scalar values are dropped.
func rawIndex(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s), true
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return strings.TrimSpace(n.String()), true
	}
	return "", false
}

func cleanSubgoalItems(items []decomposeItem) []subgoalSpec {
	out := make([]subgoalSpec, 0, len(items))
	for _, it := range items {
		if strings.TrimSpace(it.Subquestion) == "" {
			continue
		}
		out = append(out, subgoalSpec{Query: strings.TrimSpace(it.Subquestion), DependsOn: it.DependsOn})
	}
	return out
}

// buildSubgoalDAG maps subgoal specs onto a subproblemDAG keyed by the
// subgoal index. Out-of-range and self dependencies are dropped by the DAG
// constructor; cycles are broken by removing the offending edge (fail-open).
func buildSubgoalDAG(specs []subgoalSpec) *subproblemDAG {
	ids := make([]string, len(specs))
	deps := make(map[string][]string, len(specs))
	for i := range specs {
		id := strconv.Itoa(i)
		ids[i] = id
		deps[id] = append([]string(nil), specs[i].DependsOn...)
	}
	d := newSubproblemDAG(ids, deps)
	d.breakCycles()
	return d
}

func cleanStringList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
