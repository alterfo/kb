package got

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/vector"
)

func goodChunks(name string) []vector.ScoredChunk {
	return []vector.ScoredChunk{chunk(name+"-1.md", 0.9), chunk(name+"-2.md", 0.9)}
}

func baseConfig() Config {
	return Config{K: 2, CoverageHigh: 0.6, CoverageLow: 0.2, RefineCoverageThreshold: 0.5, Sleep: instantSleep}
}

func TestRunFullPipelineDecomposeToFinalize(t *testing.T) {
	retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{
		"sub1": goodChunks("sub1"),
		"sub2": goodChunks("sub2"),
	}}
	chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
		"You break a user question":         {Content: `["sub1","sub2"]`},
		"Given the original question":       {Content: `[]`},
		"You combine sub-answers":           {Content: "final aggregated answer"},
		"You answer a focused sub-question": {Content: "sub answer"},
	}}

	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	o := New(cfg)

	g := o.Run(context.Background(), "original question")

	if g.FinalAnswer != "final aggregated answer" {
		t.Fatalf("got FinalAnswer %q", g.FinalAnswer)
	}
	if g.Refined {
		t.Fatalf("got Refined=true, want false (no gaps)")
	}
	if g.Query != "original question" {
		t.Fatalf("got Query %q", g.Query)
	}

	var types []string
	for _, n := range g.Nodes {
		types = append(types, n.Type)
	}
	wantPresent := []string{NodeDecompose, NodeSubgoal, NodeAggregate, NodeFindGaps, NodeFinalize}
	for _, want := range wantPresent {
		found := false
		for _, tp := range types {
			if tp == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("node type %q missing from graph nodes %v", want, types)
		}
	}

	if len(g.Sources) != 4 {
		t.Fatalf("got %d sources, want 4 (2 per subgoal): %+v", len(g.Sources), g.Sources)
	}
}

func TestRunGatesRefineOnCoverage(t *testing.T) {
	t.Run("high coverage with gaps does not refine", func(t *testing.T) {
		retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{"sub1": goodChunks("sub1")}}
		chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
			"You break a user question":   {Content: `["sub1"]`},
			"Given the original question": {Content: `["missing detail"]`},
			"You combine sub-answers":     {Content: "draft"},
		}}
		cfg := baseConfig()
		cfg.Retriever = retriever
		cfg.Chat = chat
		g := New(cfg).Run(context.Background(), "q")

		if g.Refined {
			t.Fatalf("got Refined=true, want false (coverage above threshold)")
		}
		for _, n := range g.Nodes {
			if n.Type == NodeRefineSubgoal || n.Type == NodeRefineAggregate {
				t.Fatalf("unexpected refine node: %+v", n)
			}
		}
	})

	t.Run("low coverage with gaps refines", func(t *testing.T) {
		retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{
			"gap query": goodChunks("gap"),
		}}
		chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
			"You break a user question":   {Content: `["sub1"]`},
			"Given the original question": {Content: `["gap query"]`},
			"You combine sub-answers":     {Content: "refined final answer"},
		}}
		cfg := baseConfig()
		cfg.Retriever = retriever
		cfg.Chat = chat
		g := New(cfg).Run(context.Background(), "q")

		if !g.Refined {
			t.Fatalf("got Refined=false, want true (low coverage + gaps)")
		}
		var refineNode *Node
		for i, n := range g.Nodes {
			if n.Type == NodeRefineSubgoal {
				refineNode = &g.Nodes[i]
			}
		}
		if refineNode == nil || refineNode.Query != "gap query" {
			t.Fatalf("expected a refine_subgoal node for the gap query, got %+v", g.Nodes)
		}
		if g.FinalAnswer != "refined final answer" {
			t.Fatalf("got FinalAnswer %q", g.FinalAnswer)
		}
	})

	t.Run("low coverage with no gaps does not refine", func(t *testing.T) {
		retriever := fakeRetriever{}
		chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
			"You break a user question":   {Content: `["sub1"]`},
			"Given the original question": {Content: `[]`},
			"You combine sub-answers":     {Content: "draft"},
		}}
		cfg := baseConfig()
		cfg.Retriever = retriever
		cfg.Chat = chat
		g := New(cfg).Run(context.Background(), "q")

		if g.Refined {
			t.Fatalf("got Refined=true, want false (no gaps found)")
		}
	})
}

func TestRunExactlyOneRefine(t *testing.T) {
	gapCalls := 0
	retriever := fakeRetriever{}
	chat := countingScriptedChat{
		inner: scriptedChat{byPrompt: map[string]llm.ChatResponse{
			"You break a user question":   {Content: `["sub1"]`},
			"Given the original question": {Content: `["gap query"]`},
			"You combine sub-answers":     {Content: "answer"},
		}},
		onFindGaps: &gapCalls,
	}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	g := New(cfg).Run(context.Background(), "q")

	if gapCalls != 1 {
		t.Fatalf("find_gaps was called %d times, want exactly 1", gapCalls)
	}
	refineSubgoals := 0
	for _, n := range g.Nodes {
		if n.Type == NodeRefineSubgoal {
			refineSubgoals++
		}
	}
	if refineSubgoals != 1 {
		t.Fatalf("got %d refine_subgoal nodes, want exactly 1 (one refine pass, one gap query)", refineSubgoals)
	}
}

// countingScriptedChat wraps scriptedChat and counts find_gaps invocations
// by system-prompt prefix, so a test can assert refine runs at most once.
type countingScriptedChat struct {
	inner      scriptedChat
	onFindGaps *int
}

func (c countingScriptedChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	if len(req.Messages) > 0 && len(req.Messages[0].Content) >= len("Given the original question") &&
		req.Messages[0].Content[:len("Given the original question")] == "Given the original question" {
		*c.onFindGaps++
	}
	return c.inner.Chat(ctx, req)
}

func TestRunFailOpenOnRetrieverError(t *testing.T) {
	retriever := fakeRetriever{err: errors.New("connection refused")}
	cfg := baseConfig()
	cfg.Retriever = retriever
	g := New(cfg).Run(context.Background(), "q")

	if g.FinalAnswer == "" {
		t.Fatalf("got empty FinalAnswer, want a fail-open placeholder")
	}
	if len(g.Sources) != 0 {
		t.Fatalf("got %d sources, want 0 (retriever always errored)", len(g.Sources))
	}
}

func TestRunFailOpenOnNilChatAndRetriever(t *testing.T) {
	g := New(baseConfig()).Run(context.Background(), "q")

	if !strings.Contains(g.FinalAnswer, "no information found") {
		t.Fatalf("got FinalAnswer %q, want it to mention no information found", g.FinalAnswer)
	}
	if g.Refined {
		t.Fatalf("got Refined=true, want false")
	}
}

func TestRunFailOpenOnChatError(t *testing.T) {
	retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{"q": goodChunks("q")}}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = fakeChat{err: errors.New("boom")}
	g := New(cfg).Run(context.Background(), "q")

	if g.FinalAnswer == "" {
		t.Fatalf("got empty FinalAnswer")
	}
}

func TestRunSourcePathsPropagate(t *testing.T) {
	retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{
		"q": {{Chunk: vector.Chunk{ID: "c1", FileName: "a.md", FilePath: "notes/a.md"}, Score: 0.9}},
	}}
	cfg := baseConfig()
	cfg.Retriever = retriever
	g := New(cfg).Run(context.Background(), "q")

	if len(g.Sources) != 1 {
		t.Fatalf("got %d sources, want 1: %+v", len(g.Sources), g.Sources)
	}
	if g.Sources[0].FilePath != "notes/a.md" || g.Sources[0].FileName != "a.md" || g.Sources[0].ChunkID != "c1" {
		t.Fatalf("got %+v", g.Sources[0])
	}
}

func TestRunResultSerializesToJSON(t *testing.T) {
	retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{"q": goodChunks("q")}}
	cfg := baseConfig()
	cfg.Retriever = retriever
	g := New(cfg).Run(context.Background(), "q")

	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var round ThoughtGraph
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if round.FinalAnswer != g.FinalAnswer || len(round.Nodes) != len(g.Nodes) {
		t.Fatalf("round trip mismatch: %+v vs %+v", round, g)
	}
}

func TestRunProgressCallbacksInvoked(t *testing.T) {
	var snapshots []ThoughtGraph
	retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{"q": goodChunks("q")}}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Progress = func(g ThoughtGraph) { snapshots = append(snapshots, g) }
	final := New(cfg).Run(context.Background(), "q")

	if len(snapshots) < 6 {
		t.Fatalf("got %d progress callbacks, want at least 6 (one per stage transition)", len(snapshots))
	}
	last := snapshots[len(snapshots)-1]
	if last.FinalAnswer != final.FinalAnswer {
		t.Fatalf("last progress snapshot FinalAnswer %q != final result %q", last.FinalAnswer, final.FinalAnswer)
	}
}

func TestRunThrottlesConcurrentSubgoals(t *testing.T) {
	names := []string{"s0", "s1", "s2", "s3", "s4"}
	byQuery := map[string][]vector.ScoredChunk{}
	for _, name := range names {
		byQuery[name] = goodChunks(name)
	}
	retriever := fakeRetriever{byQuery: byQuery}
	chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
		"You break a user question": {Content: `["s0","s1","s2","s3","s4"]`},
	}}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	cfg.MaxConcurrency = 1
	g := New(cfg).Run(context.Background(), "q")

	subgoalCount := 0
	for _, n := range g.Nodes {
		if n.Type == NodeSubgoal {
			subgoalCount++
		}
	}
	if subgoalCount != 5 {
		t.Fatalf("got %d subgoal nodes, want 5", subgoalCount)
	}
}

// recordingRetriever records every retrieval query in call order and returns
// the same chunks regardless of query, so tests can assert dependency-aware
// query injection without exact-match keying.
type recordingRetriever struct {
	mu      sync.Mutex
	queries []string
}

func (r *recordingRetriever) RetrieveMode(ctx context.Context, query string, k int, mode retriever.Mode) ([]vector.ScoredChunk, error) {
	r.mu.Lock()
	r.queries = append(r.queries, query)
	r.mu.Unlock()
	return goodChunks("chunk"), nil
}

// dependencyAwareChat returns canned responses keyed by system-prompt
// prefix; synthesize answers echo the sub-question so dependent queries can
// be verified to contain their dependencies' answers.
type dependencyAwareChat struct {
	decompose string
	gaps      string
}

func (c dependencyAwareChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	sys := ""
	if len(req.Messages) > 0 {
		sys = req.Messages[0].Content
	}
	user := ""
	if len(req.Messages) > 0 {
		user = req.Messages[len(req.Messages)-1].Content
	}
	switch {
	case strings.HasPrefix(sys, "You break a user question"):
		if c.decompose != "" {
			return llm.ChatResponse{Content: c.decompose}, nil
		}
		return llm.ChatResponse{Content: `[{"subquestion":"find A"},{"subquestion":"find B","depends_on":[0]},{"subquestion":"find C","depends_on":[0,1]}]`}, nil
	case strings.HasPrefix(sys, "Given the original question"):
		if c.gaps != "" {
			return llm.ChatResponse{Content: c.gaps}, nil
		}
		return llm.ChatResponse{Content: `[]`}, nil
	case strings.HasPrefix(sys, "You combine sub-answers"):
		return llm.ChatResponse{Content: "final answer"}, nil
	case strings.HasPrefix(sys, "You answer a focused sub-question"):
		return llm.ChatResponse{Content: "answer to " + lastSubquestion(user)}, nil
	}
	return llm.ChatResponse{}, nil
}

func lastSubquestion(prompt string) string {
	sub := ""
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "Sub-question:") {
			sub = strings.TrimSpace(strings.TrimPrefix(line, "Sub-question:"))
		}
	}
	return sub
}

func TestRunDAGDependencyInjection(t *testing.T) {
	retriever := &recordingRetriever{}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = dependencyAwareChat{}
	g := New(cfg).Run(context.Background(), "q")

	if g.FinalAnswer != "final answer" {
		t.Fatalf("got FinalAnswer %q", g.FinalAnswer)
	}
	if len(retriever.queries) != 3 {
		t.Fatalf("got %d retrieval queries, want 3: %v", len(retriever.queries), retriever.queries)
	}
	if retriever.queries[0] != "find A" {
		t.Fatalf("source subgoal query = %q, want %q (no dependency prefix)", retriever.queries[0], "find A")
	}
	for _, want := range []string{"Previously resolved sub-answers:", "find A", "answer to find A", "Sub-question: find B"} {
		if !strings.Contains(retriever.queries[1], want) {
			t.Fatalf("dependent query for B missing %q:\n%s", want, retriever.queries[1])
		}
	}
	for _, want := range []string{"answer to find A", "answer to find B", "Sub-question: find C"} {
		if !strings.Contains(retriever.queries[2], want) {
			t.Fatalf("dependent query for C missing %q:\n%s", want, retriever.queries[2])
		}
	}
}

type concurrencyRetriever struct {
	mu      sync.Mutex
	current int
	max     int
}

func (c *concurrencyRetriever) RetrieveMode(ctx context.Context, query string, k int, mode retriever.Mode) ([]vector.ScoredChunk, error) {
	c.mu.Lock()
	c.current++
	if c.current > c.max {
		c.max = c.current
	}
	c.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	c.mu.Lock()
	c.current--
	c.mu.Unlock()
	return goodChunks("chunk"), nil
}

func TestRunIndependentSubgoalsRunInParallel(t *testing.T) {
	t.Run("bounded parallelism", func(t *testing.T) {
		retriever := &concurrencyRetriever{}
		chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
			"You break a user question":         {Content: `["s0","s1","s2","s3"]`},
			"Given the original question":       {Content: `[]`},
			"You combine sub-answers":           {Content: "final"},
			"You answer a focused sub-question": {Content: "sub answer"},
		}}
		cfg := baseConfig()
		cfg.Retriever = retriever
		cfg.Chat = chat
		cfg.MaxConcurrency = 3
		g := New(cfg).Run(context.Background(), "q")
		if g.FinalAnswer == "" {
			t.Fatal("expected a final answer")
		}
		if retriever.max != 3 {
			t.Fatalf("max concurrent retrievals = %d, want 3 (independent level runs in parallel)", retriever.max)
		}
	})

	t.Run("sequential when concurrency is one", func(t *testing.T) {
		retriever := &concurrencyRetriever{}
		chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
			"You break a user question":         {Content: `["s0","s1","s2"]`},
			"Given the original question":       {Content: `[]`},
			"You combine sub-answers":           {Content: "final"},
			"You answer a focused sub-question": {Content: "sub answer"},
		}}
		cfg := baseConfig()
		cfg.Retriever = retriever
		cfg.Chat = chat
		cfg.MaxConcurrency = 1
		g := New(cfg).Run(context.Background(), "q")
		if g.FinalAnswer == "" {
			t.Fatal("expected a final answer")
		}
		if retriever.max != 1 {
			t.Fatalf("max concurrent retrievals = %d, want 1", retriever.max)
		}
	})
}

func TestRunCycleFallsBackToSequentialFlat(t *testing.T) {
	retriever := &recordingRetriever{}
	chat := dependencyAwareChat{decompose: `[{"subquestion":"a","depends_on":[1]},{"subquestion":"b","depends_on":[0]}]`}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	g := New(cfg).Run(context.Background(), "q")

	if g.FinalAnswer != "final answer" {
		t.Fatalf("got FinalAnswer %q", g.FinalAnswer)
	}
	subgoalCount := 0
	for _, n := range g.Nodes {
		if n.Type == NodeSubgoal {
			subgoalCount++
			if n.Status != StatusDone {
				t.Fatalf("subgoal %q not done: %+v", n.ID, n)
			}
		}
	}
	if subgoalCount != 2 {
		t.Fatalf("got %d subgoal nodes, want 2", subgoalCount)
	}
	if len(retriever.queries) != 2 {
		t.Fatalf("got %d retrieval queries, want 2: %v", len(retriever.queries), retriever.queries)
	}
	if !strings.Contains(retriever.queries[1], "answer to b") {
		t.Fatalf("cycle-broken dependent query must contain the dependency answer, got:\n%s", retriever.queries[1])
	}
}

func TestDependencyAnswersSkipsInvalidIndices(t *testing.T) {
	results := []subgoalResult{
		{ID: "subgoal:0", Query: "a", Answer: "answer a"},
		{ID: "subgoal:1", Query: "b", Answer: "answer b"},
	}
	spec := subgoalSpec{DependsOn: []string{"0", "2", "-1", "not-a-number", "1"}}
	got := dependencyAnswers(spec, results, -1)
	if len(got) != 2 || got[0].Answer != "answer a" || got[1].Answer != "answer b" {
		t.Fatalf("got %+v, want only valid deps in declaration order", got)
	}
}

func TestDependencyAnswersSkipsSelfAndUnresolved(t *testing.T) {
	results := []subgoalResult{
		{ID: "subgoal:0", Query: "a", Answer: "answer a"},
		{},
	}
	spec := subgoalSpec{DependsOn: []string{"0", "1"}}
	got := dependencyAnswers(spec, results, 1)
	if len(got) != 1 || got[0].Answer != "answer a" {
		t.Fatalf("got %+v, want only the resolved dependency", got)
	}

	selfSpec := subgoalSpec{DependsOn: []string{"0"}}
	got = dependencyAnswers(selfSpec, results, 0)
	if len(got) != 0 {
		t.Fatalf("got %+v, want self dependency dropped", got)
	}
}

func TestFormatDependencyContextEmpty(t *testing.T) {
	if got := formatDependencyContext(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := buildDependencyAwareQuery("q", nil); got != "q" {
		t.Fatalf("got %q, want %q", got, "q")
	}
}

func TestRunGapExpansionDependsOnReporter(t *testing.T) {
	retriever := &recordingRetriever{}
	chat := dependencyAwareChat{
		decompose: `[{"subquestion":"find A"},{"subquestion":"find B","depends_on":[0]}]`,
		gaps:      `[{"subquestion":"verify C","reported_by":1}]`,
	}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	cfg.RefineCoverageThreshold = 2.0
	g := New(cfg).Run(context.Background(), "q")

	if g.FinalAnswer != "final answer" {
		t.Fatalf("got FinalAnswer %q", g.FinalAnswer)
	}
	if len(retriever.queries) != 3 {
		t.Fatalf("got %d retrieval queries, want 3: %v", len(retriever.queries), retriever.queries)
	}
	gapQuery := retriever.queries[2]
	for _, want := range []string{"Previously resolved sub-answers:", "find B", "answer to find B", "Sub-question: verify C"} {
		if !strings.Contains(gapQuery, want) {
			t.Fatalf("gap query missing %q:\n%s", want, gapQuery)
		}
	}
	refineCount := 0
	for _, n := range g.Nodes {
		if n.Type == NodeRefineSubgoal {
			refineCount++
			if n.Query != "verify C" {
				t.Fatalf("refine node query = %q, want %q", n.Query, "verify C")
			}
		}
	}
	if refineCount != 1 {
		t.Fatalf("got %d refine_subgoal nodes, want 1", refineCount)
	}
}

func TestRunGapExpansionRespectsMaxGapQueries(t *testing.T) {
	retriever := &recordingRetriever{}
	chat := dependencyAwareChat{
		decompose: `["find A"]`,
		gaps:      `[{"subquestion":"gap one","reported_by":0},{"subquestion":"gap two","reported_by":0},{"subquestion":"gap three","reported_by":0}]`,
	}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	cfg.MaxGapQueries = 2
	cfg.RefineCoverageThreshold = 2.0
	g := New(cfg).Run(context.Background(), "q")

	refineCount := 0
	for _, n := range g.Nodes {
		if n.Type == NodeRefineSubgoal {
			refineCount++
		}
	}
	if refineCount != 2 {
		t.Fatalf("got %d refine_subgoal nodes, want 2 (MaxGapQueries cap)", refineCount)
	}
}

func TestRunGapExpansionSkippedWhenCoverageHigh(t *testing.T) {
	retriever := &recordingRetriever{}
	chat := dependencyAwareChat{
		decompose: `["find A"]`,
		gaps:      `[{"subquestion":"verify C","reported_by":0}]`,
	}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	g := New(cfg).Run(context.Background(), "q")

	if g.Refined {
		t.Fatalf("got Refined=true, want false (coverage above threshold)")
	}
	for _, n := range g.Nodes {
		if n.Type == NodeRefineSubgoal || n.Type == NodeRefineAggregate {
			t.Fatalf("unexpected refine node: %+v", n)
		}
	}
}

func TestRunReportsMetricsAndDegraded(t *testing.T) {
	relevantChunk := vector.ScoredChunk{
		Chunk: vector.Chunk{ID: "c1", RefDocID: "notes/relevant", FileName: "relevant.md", Text: "text"},
		Score: 0.9,
	}
	retriever := fakeRetriever{byQuery: map[string][]vector.ScoredChunk{"sub1": {relevantChunk}}}
	chat := scriptedChat{byPrompt: map[string]llm.ChatResponse{
		"You break a user question":         {Content: `["sub1"]`},
		"Given the original question":       {Content: `[]`},
		"You combine sub-answers":           {Content: "final answer"},
		"You answer a focused sub-question": {Content: "sub answer"},
	}}
	cfg := baseConfig()
	cfg.Retriever = retriever
	cfg.Chat = chat
	cfg.RelevantIDs = []string{"notes/relevant"}
	g := New(cfg).Run(context.Background(), "original question")

	if g.Metrics.LatencyMS < 0 {
		t.Fatalf("LatencyMS = %d, want >= 0", g.Metrics.LatencyMS)
	}
	if g.Metrics.RecallAtK != 1 {
		t.Fatalf("RecallAtK = %v, want 1", g.Metrics.RecallAtK)
	}
	if g.Metrics.Cost.PromptTokens == 0 || g.Metrics.Cost.CompletionTokens == 0 {
		t.Fatalf("expected chat token cost to be recorded, got %+v", g.Metrics.Cost)
	}
}

func TestRunReportsDegradedWhenRetrieverUnavailable(t *testing.T) {
	cfg := baseConfig()
	cfg.Chat = nil
	cfg.Retriever = nil
	g := New(cfg).Run(context.Background(), "q")

	if len(g.Degraded) == 0 {
		t.Fatal("expected degraded field to be populated when retriever is unavailable")
	}
	found := false
	for _, d := range g.Degraded {
		if strings.Contains(d, "retriever unavailable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Degraded = %v, want retriever unavailable", g.Degraded)
	}
}
