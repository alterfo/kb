package got

import (
	"context"
	"strings"

	"github.com/alterfo/kb/internal/engine/retriever"
)

// subgoalSpec carries one decomposed sub-question together with the
// retrieval mode chosen for it by the decompose step.
type subgoalSpec struct {
	Query     string
	Mode      retriever.Mode
	DependsOn []string // zero-based indices of subgoals this one depends on
}

var setModeMarkers = []string{
	"сколько", "how many", "list all", "перечисли все", "number of",
}

var globalModeMarkers = []string{
	"главные тем", "основные тем", "обзор", "какие тем", "какие направления",
	"список тем", "все темы", "overview", "main themes", "key themes",
	"what topics",
}

var localModeMarkers = []string{
	"что конкретно", "как работает", "как устроен", "как устроена", "что такое",
	"что делает", "подробно", "детали", "в чем разница", "разница между",
	"compare", "details", "specifically", "how does", "what does", "explain",
}

// decomposeWithModes runs the decompose step and assigns each sub-query a
// retrieval mode.
func (o *Orchestrator) decomposeWithModes(ctx context.Context, query string) []subgoalSpec {
	specs := o.decompose(ctx, query)
	for i := range specs {
		specs[i].Mode = selectMode(specs[i].Query)
	}
	return specs
}

// selectMode picks a retrieval mode for a sub-query by keyword heuristics:
// count/list questions ("сколько", "how many", "list all", "перечисли все",
// "number of") run an exhaustive deterministic document scan (set), theme
// questions map-reduce the community hierarchy (global), concrete "about X"
// questions use the plain local pipeline, and everything else drifts from a
// community-summary seed to local refinement.
func selectMode(query string) retriever.Mode {
	q := strings.ToLower(strings.TrimSpace(query))
	if containsAny(q, setModeMarkers) {
		return retriever.ModeSet
	}
	if containsAny(q, globalModeMarkers) {
		return retriever.ModeGlobal
	}
	if containsAny(q, localModeMarkers) {
		return retriever.ModeLocal
	}
	return retriever.ModeDrift
}

func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
