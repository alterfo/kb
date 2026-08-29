package retriever

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/alterfo/kb/internal/store/vector"
)

// ModeSet runs an exhaustive multi-variant scan: it retrieves with the
// original query plus LLM expansion variants, unions the matching documents
// with per-doc caps lifted, and prepends a deterministic count summary so
// synthesis can state exact counts instead of guessing.
const ModeSet Mode = "set"

type SetResult struct {
	Count     int
	DocIDs    []string
	Saturated bool
}

func (r *Retriever) retrieveSet(ctx context.Context, query string, opt Options, k int) ([]vector.ScoredChunk, error) {
	res, evidence, err := r.SetRetrieve(ctx, query, opt.Filter)
	if err != nil {
		return nil, err
	}
	summary := vector.ScoredChunk{Chunk: setSummaryChunk(res, evidence), Score: 2}
	out := append([]vector.ScoredChunk{summary}, evidence...)
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

// SetRetrieve scans the corpus for the complete document set matching
// query+filter. Round one uses the original query; later rounds use the
// expansion sub-queries (one LLM call, fail-open) until a variant adds no
// new document or SetMaxRounds is reached. Saturated reports whether the
// union stopped growing on its own rather than by budget.
func (r *Retriever) SetRetrieve(ctx context.Context, query string, filter vector.Filter) (SetResult, []vector.ScoredChunk, error) {
	maxRounds := r.cfg.SetMaxRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}

	relaxed := r.cfg
	scanBudget := r.cfg.CandidateK * 4
	if r.cfg.Vector != nil {
		if all, err := r.cfg.Vector.AllForBM25(ctx); err == nil && len(all) > scanBudget {
			scanBudget = len(all)
		}
	}
	relaxed.CandidateK = scanBudget
	relaxed.PerDocCap = scanBudget

	subqueries := expandQuery(ctx, r.cfg.Chat, r.cfg.LLMModel, query)
	relaxed.Chat = nil
	scanner := &Retriever{cfg: relaxed}

	k := scanBudget
	best := make(map[string]vector.ScoredChunk)
	saturated := false

Loop:
	for round := 0; round < maxRounds && !saturated; round++ {
		var q string
		switch {
		case round == 0:
			q = query
		case round-1 < len(subqueries):
			q = subqueries[round-1]
		default:
			saturated = true
			break Loop
		}
		if strings.TrimSpace(q) == "" {
			saturated = true
			break
		}
		added := scanner.mergeRound(ctx, q, filter, k, best)
		if added == 0 {
			saturated = true
		}
	}

	evidence := make([]vector.ScoredChunk, 0, len(best))
	for _, sc := range best {
		evidence = append(evidence, sc)
	}
	sort.SliceStable(evidence, func(i, j int) bool {
		if evidence[i].Score != evidence[j].Score {
			return evidence[i].Score > evidence[j].Score
		}
		return evidence[i].Chunk.RefDocID < evidence[j].Chunk.RefDocID
	})

	res := SetResult{Count: len(best), Saturated: saturated}
	for _, sc := range evidence {
		if isSyntheticGraphChunk(sc) {
			continue
		}
		res.DocIDs = append(res.DocIDs, sc.Chunk.RefDocID)
	}
	return res, evidence, nil
}

func isSyntheticGraphChunk(sc vector.ScoredChunk) bool {
	if sc.Chunk.ID == "set-summary" && sc.Chunk.RefDocID == "set-summary" {
		return true
	}
	id := sc.Chunk.RefDocID
	return strings.HasPrefix(id, "community:") || strings.HasPrefix(id, "global:")
}

func (r *Retriever) mergeRound(ctx context.Context, query string, filter vector.Filter, k int, best map[string]vector.ScoredChunk) int {
	chunks, err := r.retrieveLocal(ctx, query, Options{K: k, Filter: filter}, k)
	if err != nil {
		return 0
	}
	added := 0
	for _, sc := range chunks {
		if isSyntheticGraphChunk(sc) {
			continue
		}
		prev, ok := best[sc.Chunk.RefDocID]
		if !ok {
			best[sc.Chunk.RefDocID] = sc
			added++
			continue
		}
		if sc.Score > prev.Score {
			best[sc.Chunk.RefDocID] = sc
		}
	}
	return added
}

func setSummaryChunk(res SetResult, evidence []vector.ScoredChunk) vector.Chunk {
	var names []string
	seen := make(map[string]struct{})
	for _, ev := range evidence {
		n := ev.Chunk.FileName
		if n == "" {
			n = ev.Chunk.RefDocID
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		names = append(names, n)
		if len(names) >= 50 {
			break
		}
	}
	text := fmt.Sprintf("Deterministic document scan matched %d documents (complete scan: %v): %s.",
		res.Count, res.Saturated, strings.Join(names, "; "))
	return vector.Chunk{
		ID:       "set-summary",
		RefDocID: "set-summary",
		Text:     text,
		FilePath: "scan/set-summary",
		FileName: "set-summary",
		Source:   "scan",
	}
}
