// Package got implements the Graph-of-Thoughts orchestrator: a query is
// decomposed into sub-questions answered in parallel against the
// graph-aware retriever, aggregated into a draft answer, checked for gaps,
// optionally refined once, then finalized with source citations. Every
// stage is fail-open: a retriever or LLM error degrades that stage's
// output rather than aborting the run.
package got

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

// Retriever runs graph-aware retrieval for a single query. Satisfied by an
// adapter around *retriever.Retriever.
type Retriever interface {
	RetrieveMode(ctx context.Context, query string, k int, mode retriever.Mode) ([]vector.ScoredChunk, error)
}

// filterAwareRetriever is the optional Retriever extension used when the
// run carries a structured filter; production adapters implement it, test
// fakes may keep relying on plain RetrieveMode.
type filterAwareRetriever interface {
	RetrieveModeFiltered(ctx context.Context, query string, k int, mode retriever.Mode, filter vector.Filter) ([]vector.ScoredChunk, error)
}

// ChatClient runs a single chat completion. Satisfied by *llm.Client.
type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

// Config wires the orchestrator's dependencies and tunables. Retriever and
// Chat are both optional; a nil Retriever degrades every subgoal to an
// empty result, a nil Chat skips decomposition/synthesis/judging/gap-
// finding and falls back to deterministic text.
type Config struct {
	Retriever Retriever
	Chat      ChatClient
	Model     string

	// Filter constrains retrieval calls of the run (AND semantics). Filtering
	// requires a filter-aware Retriever (the production adapter); a Retriever
	// exposing only the base RetrieveMode interface silently ignores it.
	Filter vector.Filter
	// ExtractQualifiers enables one LLM qualifier-extraction pass over the
	// top-level question per Run; extracted qualifiers merge over Filter.
	ExtractQualifiers bool
	// AbstainThreshold (0..1] enables the abstention verdict: when every
	// subgoal ends uncovered and average coverage sits below the threshold,
	// the run answers "not found" instead of a low-confidence draft. 0
	// keeps legacy behavior.
	AbstainThreshold float64

	K              int // chunks retrieved per subgoal
	MaxSubgoals    int
	MaxConcurrency int
	MaxGapQueries  int
	RollingMemory  int

	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Sleep      func(ctx context.Context, d time.Duration) error
	JitterFunc func() float64

	CoverageHigh            float64 // deterministic score at/above which a subgoal is covered outright
	CoverageLow             float64 // deterministic score at/below which a subgoal is uncovered outright
	RefineCoverageThreshold float64 // overall coverage below which a refine pass is attempted

	ContradictionDetector ContradictionDetector
	DetectContradictions  bool

	Progress ProgressFunc
}

type Orchestrator struct {
	cfg Config
}

func New(cfg Config) *Orchestrator {
	if cfg.K <= 0 {
		cfg.K = 8
	}
	if cfg.MaxSubgoals <= 0 {
		cfg.MaxSubgoals = 5
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 3
	}
	if cfg.MaxGapQueries <= 0 {
		cfg.MaxGapQueries = 3
	}
	if cfg.RollingMemory <= 0 {
		cfg.RollingMemory = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 2 * time.Second
	}
	if cfg.Sleep == nil {
		cfg.Sleep = defaultSleep
	}
	if cfg.JitterFunc == nil {
		cfg.JitterFunc = rand.Float64
	}
	if cfg.CoverageHigh <= 0 {
		cfg.CoverageHigh = 0.6
	}
	if cfg.CoverageLow <= 0 {
		cfg.CoverageLow = 0.2
	}
	if cfg.RefineCoverageThreshold <= 0 {
		cfg.RefineCoverageThreshold = 0.5
	}
	return &Orchestrator{cfg: cfg}
}

// subgoalResult is the outcome of one retrieve->score_coverage->synthesize
// branch, carried forward into aggregation and, if a gap query, into a
// second aggregation after refine.
type subgoalResult struct {
	ID       string
	Query    string
	Answer   string
	Coverage float64
	Covered  bool
	Sources  []Source
	Deps     []string

	Contradictions []string
}

// Run executes decompose -> parallel[retrieve -> score_coverage ->
// synthesize] -> aggregate -> find_gaps -> at most one refine ->
// finalize. It never returns an error: every stage degrades on failure, so
// the returned ThoughtGraph always carries a FinalAnswer (possibly a
// fail-open placeholder) and whatever Sources were found.
func (o *Orchestrator) Run(ctx context.Context, query string) ThoughtGraph {
	b := newGraphBuilder(query, o.cfg.Progress)

	b.setNode(Node{ID: NodeDecompose, Type: NodeDecompose, Query: query, Status: StatusRunning})
	subgoals := o.decomposeWithModes(ctx, query)
	b.setNode(Node{ID: NodeDecompose, Type: NodeDecompose, Query: query, Status: StatusDone})
	b.setNode(Node{ID: NodePlan, Type: NodePlan, ParentID: NodeDecompose, Query: planQuery(subgoals), Status: StatusDone})

	runFilter := o.cfg.Filter
	if o.cfg.ExtractQualifiers {
		if extracted, ok := retriever.ExtractQualifiers(ctx, o.cfg.Chat, o.cfg.Model, query); ok {
			runFilter = vector.MergeAND(runFilter, extracted)
		}
	}

	results := o.runSubgoalsScheduled(ctx, b, NodeDecompose, NodeSubgoal, subgoals, runFilter)

	b.setNode(Node{ID: NodeAggregate, Type: NodeAggregate, ParentID: NodeDecompose, Status: StatusRunning})
	draft := o.aggregate(ctx, query, results)
	b.setNode(Node{
		ID: NodeAggregate, Type: NodeAggregate, ParentID: NodeDecompose,
		Status: StatusDone, Answer: draft, Sources: dedupSources(allSources(results)),
	})

	b.setNode(Node{ID: NodeFindGaps, Type: NodeFindGaps, ParentID: NodeAggregate, Status: StatusRunning})
	gaps := o.findGaps(ctx, query, draft)
	b.setNode(Node{
		ID: NodeFindGaps, Type: NodeFindGaps, ParentID: NodeAggregate,
		Status: StatusDone, Answer: joinGapList(gaps),
	})

	finalAnswer := draft
	allResults := results
	refined := false

	if o.shouldRefine(gaps, results) {
		if len(gaps) > o.cfg.MaxGapQueries {
			gaps = gaps[:o.cfg.MaxGapQueries]
		}
		refineSubgoals := buildExpansionSubgoals(gaps, len(results))
		refineResults := o.runExpansionScheduled(ctx, b, results, refineSubgoals, runFilter)
		allResults = append(append([]subgoalResult(nil), results...), refineResults...)

		b.setNode(Node{ID: NodeRefineAggregate, Type: NodeRefineAggregate, ParentID: NodeFindGaps, Status: StatusRunning})
		finalAnswer = o.aggregate(ctx, query, allResults)
		refined = true
		b.setNode(Node{
			ID: NodeRefineAggregate, Type: NodeRefineAggregate, ParentID: NodeFindGaps,
			Status: StatusDone, Answer: finalAnswer, Sources: dedupSources(allSources(refineResults)),
		})
	}

	if o.cfg.AbstainThreshold > 0 && allUncovered(allResults) && averageCoverage(allResults) < o.cfg.AbstainThreshold {
		finalAnswer = abstainFinalAnswer
	}

	finalSources := dedupSources(allSources(allResults))
	b.setNode(Node{ID: NodeFinalize, Type: NodeFinalize, Status: StatusRunning})
	b.setFinal(refined, finalAnswer, finalSources)
	b.setNode(Node{ID: NodeFinalize, Type: NodeFinalize, Status: StatusDone, Answer: finalAnswer, Sources: finalSources})

	return b.snapshot()
}

// planQuery renders the topo-ordered subproblem sequence for the plan node.
// The DAG construction breaks cycles first; a defensive topo-sort failure
// falls back to the original slice order (fail-open).
func planQuery(subgoals []subgoalSpec) string {
	dag := buildSubgoalDAG(subgoals)
	order, err := dag.topoSort()
	if err != nil {
		order = make([]string, len(subgoals))
		for i := range subgoals {
			order[i] = strconv.Itoa(i)
		}
	}
	var b strings.Builder
	for i, id := range order {
		idx, convErr := strconv.Atoi(id)
		if convErr != nil || idx < 0 || idx >= len(subgoals) {
			continue
		}
		if i > 0 {
			b.WriteString(" -> ")
		}
		b.WriteString(subgoals[idx].Query)
	}
	return b.String()
}

// shouldRefine gates the single refine pass on both signal: find_gaps must
// have surfaced at least one open question, and the average subgoal
// coverage must sit below the configured threshold. Either signal alone is
// not enough: gaps with strong coverage are treated as nice-to-have, and
// weak coverage with no articulated gap has nothing concrete to refine.
func (o *Orchestrator) shouldRefine(gaps []gapSpec, results []subgoalResult) bool {
	if len(gaps) == 0 {
		return false
	}
	return averageCoverage(results) < o.cfg.RefineCoverageThreshold
}

func averageCoverage(results []subgoalResult) float64 {
	if len(results) == 0 {
		return 0
	}
	var sum float64
	for _, r := range results {
		sum += r.Coverage
	}
	return sum / float64(len(results))
}

func allSources(results []subgoalResult) []Source {
	var out []Source
	for _, r := range results {
		out = append(out, r.Sources...)
	}
	return out
}

func joinGapList(gaps []gapSpec) string {
	out := ""
	for i, g := range gaps {
		if i > 0 {
			out += "; "
		}
		out += g.Query
	}
	return out
}

// buildExpansionSubgoals turns gap specs into subgoal specs whose DependsOn
// references the zero-based index of the original subgoal that reported the
// gap. Gaps without a valid reporter stay root-level (no dependencies).
func buildExpansionSubgoals(gaps []gapSpec, numResults int) []subgoalSpec {
	out := make([]subgoalSpec, 0, len(gaps))
	for _, g := range gaps {
		spec := subgoalSpec{Query: g.Query, Mode: selectMode(g.Query)}
		if g.ReportedBy >= 0 && g.ReportedBy < numResults {
			spec.DependsOn = []string{strconv.Itoa(g.ReportedBy)}
		}
		out = append(out, spec)
	}
	return out
}

// runExpansionScheduled inserts the gap subproblems into the subgoal DAG as
// new nodes depending on their reporter, re-toposorts the combined graph,
// and solves only the new nodes in level order. Original results are seeded
// as already-done nodes so a gap resolved after its reporter sees the
// reporter's answer in its dependency context.
func (o *Orchestrator) runExpansionScheduled(ctx context.Context, b *graphBuilder, results []subgoalResult, gaps []subgoalSpec, filter vector.Filter) []subgoalResult {
	if len(gaps) == 0 {
		return nil
	}

	ids := make([]string, 0, len(results)+len(gaps))
	deps := make(map[string][]string, len(results)+len(gaps))
	for i, r := range results {
		id := strconv.Itoa(i)
		ids = append(ids, id)
		deps[id] = append([]string(nil), r.Deps...)
	}
	gapIDs := make([]string, len(gaps))
	for i, g := range gaps {
		id := "gap:" + strconv.Itoa(i)
		gapIDs[i] = id
		ids = append(ids, id)
		deps[id] = append([]string(nil), g.DependsOn...)
	}
	dag := newSubproblemDAG(ids, deps)
	dag.breakCycles()

	levels, err := dag.levels()
	if err != nil {
		return o.runExpansionSequential(ctx, b, results, gaps, filter)
	}

	resolved := make(map[string]subgoalResult, len(results)+len(gaps))
	for i, r := range results {
		resolved[strconv.Itoa(i)] = r
	}

	maxLevel := 0
	for _, lvl := range levels {
		if lvl > maxLevel {
			maxLevel = lvl
		}
	}

	gapResults := make([]subgoalResult, len(gaps))
	memory := newRollingMemory(o.cfg.RollingMemory)
	for _, r := range results {
		memory.add(r)
	}

	for lvl := 0; lvl <= maxLevel; lvl++ {
		var gapIndices []int
		for i, id := range gapIDs {
			if levels[id] == lvl {
				gapIndices = append(gapIndices, i)
			}
		}
		if len(gapIndices) == 0 {
			continue
		}
		o.runExpansionLevel(ctx, b, gaps, gapIndices, resolved, gapResults, memory.snapshot(), filter)
		for _, i := range gapIndices {
			resolved[gapIDs[i]] = gapResults[i]
			memory.add(gapResults[i])
		}
	}
	return gapResults
}

// runExpansionLevel resolves one level of new gap nodes in parallel bounded
// by MaxConcurrency. All dependency nodes (original or earlier gaps) are
// already present in resolved.
func (o *Orchestrator) runExpansionLevel(ctx context.Context, b *graphBuilder, gaps []subgoalSpec, indices []int, resolved map[string]subgoalResult, gapResults []subgoalResult, memory []subgoalResult, filter vector.Filter) {
	sem := make(chan struct{}, o.cfg.MaxConcurrency)
	var wg sync.WaitGroup
	for _, i := range indices {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			spec := gaps[i]
			id := fmt.Sprintf("%s:%d", NodeRefineSubgoal, i)
			deps := expansionDependencyAnswers(spec, resolved)
			gapResults[i] = o.runSubgoal(ctx, b, id, NodeFindGaps, NodeRefineSubgoal, spec, deps, memory, filter)
		}(i)
	}
	wg.Wait()
}

// runExpansionSequential is the cycle fallback for the expansion DAG: it
// resolves gap nodes one by one in slice order, still honoring reporter
// dependencies where their answers are already available.
func (o *Orchestrator) runExpansionSequential(ctx context.Context, b *graphBuilder, results []subgoalResult, gaps []subgoalSpec, filter vector.Filter) []subgoalResult {
	resolved := make(map[string]subgoalResult, len(results)+len(gaps))
	for i, r := range results {
		resolved[strconv.Itoa(i)] = r
	}
	gapResults := make([]subgoalResult, len(gaps))
	for i, spec := range gaps {
		id := fmt.Sprintf("%s:%d", NodeRefineSubgoal, i)
		deps := expansionDependencyAnswers(spec, resolved)
		gapResults[i] = o.runSubgoal(ctx, b, id, NodeFindGaps, NodeRefineSubgoal, spec, deps, nil, filter)
		resolved["gap:"+strconv.Itoa(i)] = gapResults[i]
	}
	return gapResults
}

// expansionDependencyAnswers resolves spec.DependsOn keys against the
// combined-DAG node results. Missing keys are skipped (fail-open).
func expansionDependencyAnswers(spec subgoalSpec, resolved map[string]subgoalResult) []subgoalResult {
	var out []subgoalResult
	for _, dep := range spec.DependsOn {
		key := strings.TrimSpace(dep)
		if r, ok := resolved[key]; ok {
			out = append(out, r)
		}
	}
	return out
}

// runSubgoalsScheduled solves subgoals in dependency order. Nodes are
// partitioned into longest-path levels from the DAG sources: every node of
// one level resolves only after all lower levels are done, while nodes
// within a level run in parallel under MaxConcurrency. A subgoal's
// retrieval query is prefixed with the resolved answers of its
// dependencies.
func (o *Orchestrator) runSubgoalsScheduled(ctx context.Context, b *graphBuilder, parentID, nodeType string, subgoals []subgoalSpec, filter vector.Filter) []subgoalResult {
	results := make([]subgoalResult, len(subgoals))
	if len(subgoals) == 0 {
		return results
	}

	dag := buildSubgoalDAG(subgoals)
	levels, err := dag.levels()
	if err != nil {
		// buildSubgoalDAG already breaks cycles, so this is a defensive
		// fail-open guard: degrade to a deterministic sequential flat run.
		return o.runSubgoalsSequential(ctx, b, parentID, nodeType, subgoals, filter)
	}

	maxLevel := 0
	for _, lvl := range levels {
		if lvl > maxLevel {
			maxLevel = lvl
		}
	}
	byLevel := make([][]int, maxLevel+1)
	for i := range subgoals {
		lvl := levels[strconv.Itoa(i)]
		byLevel[lvl] = append(byLevel[lvl], i)
	}

	memory := newRollingMemory(o.cfg.RollingMemory)
	for lvl := 0; lvl <= maxLevel; lvl++ {
		o.runSubgoalLevel(ctx, b, parentID, nodeType, subgoals, byLevel[lvl], results, memory.snapshot(), filter)
		for _, i := range byLevel[lvl] {
			memory.add(results[i])
		}
	}
	return results
}

// runSubgoalLevel resolves one level of the DAG. All dependencies of these
// nodes live in lower levels and are already resolved, so the whole level
// may run in parallel bounded by MaxConcurrency.
func (o *Orchestrator) runSubgoalLevel(ctx context.Context, b *graphBuilder, parentID, nodeType string, subgoals []subgoalSpec, indices []int, results []subgoalResult, memory []subgoalResult, filter vector.Filter) {
	sem := make(chan struct{}, o.cfg.MaxConcurrency)
	var wg sync.WaitGroup
	for _, i := range indices {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			spec := subgoals[i]
			id := fmt.Sprintf("%s:%d", nodeType, i)
			deps := dependencyAnswers(spec, results, i)
			results[i] = o.runSubgoal(ctx, b, id, parentID, nodeType, spec, deps, memory, filter)
		}(i)
	}
	wg.Wait()
}

// runSubgoalsSequential is the cycle fallback: resolve each subgoal one by
// one in slice order, ignoring dependencies. It is kept as a fail-open
// guard; buildSubgoalDAG normally breaks cycles before scheduling.
func (o *Orchestrator) runSubgoalsSequential(ctx context.Context, b *graphBuilder, parentID, nodeType string, subgoals []subgoalSpec, filter vector.Filter) []subgoalResult {
	results := make([]subgoalResult, len(subgoals))
	for i, spec := range subgoals {
		id := fmt.Sprintf("%s:%d", nodeType, i)
		results[i] = o.runSubgoal(ctx, b, id, parentID, nodeType, spec, nil, nil, filter)
	}
	return results
}

// dependencyAnswers resolves spec.DependsOn (zero-based indices into the
// subgoal slice) into the already-computed results, in declaration order.
// Invalid, out-of-range and self indices are skipped (fail-open).
func dependencyAnswers(spec subgoalSpec, results []subgoalResult, self int) []subgoalResult {
	var out []subgoalResult
	for _, dep := range spec.DependsOn {
		idx, err := strconv.Atoi(strings.TrimSpace(dep))
		if err != nil || idx < 0 || idx >= len(results) || idx == self {
			continue
		}
		if results[idx].ID == "" {
			continue
		}
		out = append(out, results[idx])
	}
	return out
}

// formatDependencyContext renders resolved dependency answers for injection
// into retrieval and synthesis prompts. It is empty when there are no deps.
func formatDependencyContext(deps []subgoalResult) string {
	resolved := make([]subgoalResult, 0, len(deps))
	for _, d := range deps {
		if d.ID == "" && d.Query == "" {
			continue
		}
		resolved = append(resolved, d)
	}
	if len(resolved) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Previously resolved sub-answers:\n")
	for _, d := range resolved {
		fmt.Fprintf(&b, "- %s: %s\n", d.Query, d.Answer)
	}
	return b.String()
}

// buildDependencyAwareQuery prefixes query with resolved dependency answers
// so a dependent subproblem's retrieval can use earlier reasoning results.
func buildDependencyAwareQuery(query string, deps []subgoalResult) string {
	if ctx := formatDependencyContext(deps); ctx != "" {
		return ctx + "\nSub-question: " + query
	}
	return query
}

func (o *Orchestrator) runSubgoal(ctx context.Context, b *graphBuilder, id, parentID, nodeType string, spec subgoalSpec, deps []subgoalResult, memory []subgoalResult, filter vector.Filter) subgoalResult {
	b.setNode(Node{ID: id, Type: nodeType, ParentID: parentID, Query: spec.Query, Deps: append([]string(nil), spec.DependsOn...), Status: StatusRunning, Stage: StageRetrieving})

	retrievalQuery := buildDependencyAwareQuery(spec.Query, deps)
	chunks := o.retrieve(ctx, retrievalQuery, spec.Mode, filter)
	contradictions := o.detectContradictions(ctx, spec.Query, chunks)

	b.setStage(id, StageScoringCoverage)
	cov := o.scoreCoverage(ctx, spec.Query, chunks)

	b.setStage(id, StageSynthesizing)
	answer := o.synthesize(ctx, spec.Query, chunks, deps, memory)

	sources := sourcesFromChunks(chunks)
	b.setNode(Node{
		ID: id, Type: nodeType, ParentID: parentID, Query: spec.Query,
		Deps:   append([]string(nil), spec.DependsOn...),
		Status: StatusDone, Stage: StageDone,
		Coverage: cov.Score, Covered: cov.Covered, Answer: answer, Sources: sources,
		Contradictions: contradictions,
	})

	return subgoalResult{ID: id, Query: spec.Query, Answer: answer, Coverage: cov.Score, Covered: cov.Covered, Sources: sources, Deps: append([]string(nil), spec.DependsOn...), Contradictions: contradictions}
}

// retrieve calls the retriever with retry+backoff, failing open to nil
// chunks once retries are exhausted.
func (o *Orchestrator) retrieve(ctx context.Context, query string, mode retriever.Mode, filter vector.Filter) []vector.ScoredChunk {
	if o.cfg.Retriever == nil {
		return nil
	}
	for attempt := 0; attempt <= o.cfg.MaxRetries; attempt++ {
		chunks, err := o.retrieverRetrieve(ctx, query, mode, filter)
		if err == nil {
			return chunks
		}
		if attempt == o.cfg.MaxRetries || !o.wait(ctx, attempt) {
			break
		}
	}
	return nil
}

func (o *Orchestrator) retrieverRetrieve(ctx context.Context, query string, mode retriever.Mode, filter vector.Filter) ([]vector.ScoredChunk, error) {
	if fa, ok := o.cfg.Retriever.(filterAwareRetriever); ok {
		return fa.RetrieveModeFiltered(ctx, query, o.cfg.K, mode, filter)
	}
	return o.cfg.Retriever.RetrieveMode(ctx, query, o.cfg.K, mode)
}

// chat calls Chat with retry+backoff, failing open to (zero, false) once
// retries are exhausted.
func (o *Orchestrator) chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, bool) {
	if o.cfg.Chat == nil {
		return llm.ChatResponse{}, false
	}
	var lastErr error
	for attempt := 0; attempt <= o.cfg.MaxRetries; attempt++ {
		resp, err := o.cfg.Chat.Chat(ctx, req)
		if err == nil {
			return resp, true
		}
		lastErr = err
		if attempt == o.cfg.MaxRetries || !o.wait(ctx, attempt) {
			break
		}
	}
	slog.Error("chat request failed", "error", lastErr)
	return llm.ChatResponse{}, false
}

func (o *Orchestrator) wait(ctx context.Context, attempt int) bool {
	delay := o.backoffDelay(attempt)
	return o.cfg.Sleep(ctx, delay) == nil
}

func (o *Orchestrator) backoffDelay(attempt int) time.Duration {
	cap := o.cfg.BaseDelay << uint(attempt)
	if cap <= 0 || cap > o.cfg.MaxDelay {
		cap = o.cfg.MaxDelay
	}
	return time.Duration(o.cfg.JitterFunc() * float64(cap))
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
