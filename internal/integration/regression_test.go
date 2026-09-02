package integration

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
	"github.com/alterfo/kb/internal/testkit"
)

// regressionCorpus is a small deterministic corpus whose documents have
// clearly separated topics, so the fake embedder and BM25 rank them without
// an LLM or network. The query-aware fake chat below keeps query expansion
// deterministic and query-sensitive instead of returning testkit's canned
// sub-queries for every input.
func regressionDocuments() []connector.Document {
	return []connector.Document{
		{
			Source: "notes",
			ID:     "retriever",
			Body:   "The kb project is a graph based knowledge base. The retriever module fuses dense vectors and BM25 scores. Alice maintains the retriever module.",
		},
		{
			Source: "notes",
			ID:     "indexer",
			Body:   "The indexer ingests documents and chunks them into sentences. Bob wrote the indexer module.",
		},
		{
			Source: "notes",
			ID:     "graphstore",
			Body:   "The knowledge graph stores entities and relations between them. Carol maintains the graph store.",
		},
		{
			Source: "notes",
			ID:     "governance",
			Body:   "The governance subsystem scans the corpus and applies retention policy. Dan owns governance.",
		},
	}
}

func indexRegressionCorpus(t *testing.T, p *fakePipeline) {
	t.Helper()
	ctx := context.Background()
	for _, doc := range regressionDocuments() {
		if err := p.idx.IndexDocument(ctx, doc); err != nil {
			t.Fatalf("IndexDocument(%s): %v", doc.ID, err)
		}
	}
}

// queryAwareChat wraps the testkit fake chat and overrides query expansion
// so the retriever's dense leg ranks by the literal query. Every other LLM
// prompt (graph extraction, summarization, decomposition, synthesis) still
// delegates to the deterministic FakeChat.
type queryAwareChat struct {
	testkit.FakeChat
}

func (c queryAwareChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, "rewrite a search query into 3-5") {
			return c.expand(req)
		}
	}
	return c.FakeChat.Chat(ctx, req)
}

func (c queryAwareChat) expand(req llm.ChatRequest) (llm.ChatResponse, error) {
	query := ""
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			query = msg.Content
		}
	}
	subqueries := []string{}
	if strings.TrimSpace(query) != "" {
		subqueries = append(subqueries, query)
	}
	raw, err := json.Marshal(subqueries)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	return llm.ChatResponse{Content: string(raw), FinishReason: "stop"}, nil
}

// regressionRetriever rebuilds BM25 from the indexed corpus and returns a
// hybrid retriever wired to the pipeline stores and the supplied chat.
func regressionRetriever(t *testing.T, p *fakePipeline, chat retriever.ChatClient, embed testkit.FakeEmbedder) *retriever.Retriever {
	t.Helper()
	ctx := context.Background()
	chunks, err := p.vs.AllForBM25(ctx)
	if err != nil {
		t.Fatalf("AllForBM25: %v", err)
	}
	version, err := p.db.CorpusVersion(ctx)
	if err != nil {
		t.Fatalf("CorpusVersion: %v", err)
	}
	b := bm25.New()
	b.Rebuild(chunks, version)
	return retriever.New(retriever.Config{
		Vector:         p.vs,
		BM25:           b,
		Chat:           chat,
		Embed:          embed,
		Graph:          p.gs,
		LLMModel:       "test",
		EmbedModel:     "test",
		Hybrid:         true,
		AuthorityBonus: map[string]float64{},
		RRFK:           60,
	})
}

type goldenQuery struct {
	name      string
	query     string
	relevant  []string // RefDocIDs that must be retrieved
	k         int
	minRecall float64
}

var regressionGoldenQueries = []goldenQuery{
	{name: "retriever", query: "who maintains the retriever module", relevant: []string{"notes/retriever"}, k: 3, minRecall: 1.0},
	{name: "indexer", query: "who wrote the indexer", relevant: []string{"notes/indexer"}, k: 3, minRecall: 1.0},
	{name: "graphstore", query: "who maintains the graph store", relevant: []string{"notes/graphstore"}, k: 3, minRecall: 1.0},
	{name: "governance", query: "who owns governance", relevant: []string{"notes/governance"}, k: 3, minRecall: 1.0},
}

func recallAtK(hits []vector.ScoredChunk, relevant map[string]struct{}, k int) float64 {
	if len(relevant) == 0 {
		return 1
	}
	if k > len(hits) {
		k = len(hits)
	}
	found := 0
	for i := 0; i < k; i++ {
		if _, ok := relevant[hits[i].RefDocID]; ok {
			found++
		}
	}
	return float64(found) / float64(len(relevant))
}

func precisionAtK(hits []vector.ScoredChunk, relevant map[string]struct{}, k int) float64 {
	if k > len(hits) {
		k = len(hits)
	}
	if k == 0 {
		return 0
	}
	found := 0
	for i := 0; i < k; i++ {
		if _, ok := relevant[hits[i].RefDocID]; ok {
			found++
		}
	}
	return float64(found) / float64(k)
}

func firstRefDocID(hits []vector.ScoredChunk) string {
	if len(hits) == 0 {
		return "<none>"
	}
	return hits[0].RefDocID
}

// TestRegressionGoldenQueriesRecallPrecision is the deterministic retrieval
// quality gate: every golden query must find its relevant document in the
// top-k (recall) and rank it first (precision@1), using only the testkit
// fake embedder/chat and the deterministic hybrid retriever.
func TestRegressionGoldenQueriesRecallPrecision(t *testing.T) {
	chat := testkit.NewFakeChat()
	embed := testkit.NewFakeEmbedder()
	p := newFakePipeline(t, chat, embed)
	indexRegressionCorpus(t, p)
	r := regressionRetriever(t, p, queryAwareChat{FakeChat: chat}, embed)
	ctx := context.Background()

	for _, gq := range regressionGoldenQueries {
		gq := gq
		t.Run(gq.name, func(t *testing.T) {
			hits, err := r.Retrieve(ctx, gq.query, retriever.Options{K: gq.k})
			if err != nil {
				t.Fatalf("Retrieve: %v", err)
			}
			relevant := make(map[string]struct{}, len(gq.relevant))
			for _, id := range gq.relevant {
				relevant[id] = struct{}{}
			}

			recall := recallAtK(hits, relevant, gq.k)
			if recall < gq.minRecall {
				t.Fatalf("recall@%d = %.3f, want >= %.3f (retrieved %d chunks)", gq.k, recall, gq.minRecall, len(hits))
			}
			if prec := precisionAtK(hits, relevant, 1); prec < 1 {
				t.Fatalf("precision@1 = %.3f, want 1.0: top hit %q is not relevant", prec, firstRefDocID(hits))
			}
		})
	}
}

// TestRegressionGoTThoughtGraphDAGInvariants runs the orchestrator against
// the deterministic fake-LLM pipeline and checks structural invariants of
// the resulting thought graph rather than exact strings: unique node IDs,
// no dangling or self parents, an acyclic parent graph, exactly one of each
// singleton stage, finished statuses, and a valid acyclic subgoal dependency
// graph.
func TestRegressionGoTThoughtGraphDAGInvariants(t *testing.T) {
	chat := testkit.NewFakeChat()
	embed := testkit.NewFakeEmbedder()
	p := newFakePipeline(t, chat, embed)
	indexRegressionCorpus(t, p)
	r := regressionRetriever(t, p, queryAwareChat{FakeChat: chat}, embed)

	orch := got.New(got.Config{
		Retriever:      retriever.Adapter{Retriever: r},
		Chat:           chat,
		Model:          "test",
		K:              4,
		MaxSubgoals:    2,
		MaxConcurrency: 2,
	})
	tg := orch.Run(context.Background(), "what is the kb project and who maintains its retriever module")
	assertThoughtGraphDAGInvariants(t, tg)
}

func assertThoughtGraphDAGInvariants(t *testing.T, tg got.ThoughtGraph) {
	t.Helper()
	if len(tg.Nodes) == 0 {
		t.Fatal("thought graph has no nodes")
	}

	byID := make(map[string]got.Node, len(tg.Nodes))
	required := map[string]int{
		got.NodeDecompose: 0,
		got.NodePlan:      0,
		got.NodeAggregate: 0,
		got.NodeFindGaps:  0,
		got.NodeFinalize:  0,
	}
	for _, n := range tg.Nodes {
		if _, dup := byID[n.ID]; dup {
			t.Fatalf("duplicate node id %q", n.ID)
		}
		byID[n.ID] = n
		if _, ok := required[n.Type]; ok {
			required[n.Type]++
		}
	}

	for _, nodeType := range []string{got.NodeDecompose, got.NodePlan, got.NodeAggregate, got.NodeFindGaps, got.NodeFinalize} {
		if required[nodeType] != 1 {
			t.Errorf("expected exactly one %q node, got %d", nodeType, required[nodeType])
		}
	}

	for _, n := range tg.Nodes {
		if n.Status != got.StatusDone {
			t.Errorf("node %q has status %q, want %q", n.ID, n.Status, got.StatusDone)
		}
		if n.ParentID == "" {
			continue
		}
		if n.ParentID == n.ID {
			t.Errorf("node %q is its own parent", n.ID)
		}
		if _, ok := byID[n.ParentID]; !ok {
			t.Errorf("node %q has dangling parent %q", n.ID, n.ParentID)
		}
	}

	assertParentGraphAcyclic(t, tg.Nodes)
	assertSubgoalDependencyGraphValid(t, tg.Nodes)
}

func assertParentGraphAcyclic(t *testing.T, nodes []got.Node) {
	t.Helper()
	edges := make(map[string][]string)
	for _, n := range nodes {
		if n.ParentID == "" {
			continue
		}
		edges[n.ParentID] = append(edges[n.ParentID], n.ID)
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(nodes))
	var visit func(string) bool
	visit = func(id string) bool {
		switch color[id] {
		case gray:
			return false
		case black:
			return true
		}
		color[id] = gray
		for _, child := range edges[id] {
			if !visit(child) {
				return false
			}
		}
		color[id] = black
		return true
	}
	for _, n := range nodes {
		if !visit(n.ID) {
			t.Fatalf("parent graph is cyclic near node %q", n.ID)
		}
	}
}

func assertSubgoalDependencyGraphValid(t *testing.T, nodes []got.Node) {
	t.Helper()
	byIndex := map[int]got.Node{}
	maxIndex := -1
	for _, n := range nodes {
		if n.Type != got.NodeSubgoal {
			continue
		}
		idx, ok := subgoalIndex(n.ID)
		if !ok {
			t.Fatalf("subgoal node %q has non-index id", n.ID)
		}
		if _, dup := byIndex[idx]; dup {
			t.Fatalf("duplicate subgoal index %d", idx)
		}
		byIndex[idx] = n
		if idx > maxIndex {
			maxIndex = idx
		}
	}

	subgoals := make([]got.Node, maxIndex+1)
	for i := range subgoals {
		n, ok := byIndex[i]
		if !ok {
			t.Fatalf("subgoal indices are not contiguous: missing %d", i)
		}
		subgoals[i] = n
	}

	for i, sg := range subgoals {
		seen := map[int]struct{}{}
		for _, raw := range sg.Deps {
			if raw == "" {
				continue
			}
			dep, err := strconv.Atoi(raw)
			if err != nil || dep < 0 || dep >= len(subgoals) {
				t.Errorf("subgoal %q has invalid dependency %q", sg.ID, raw)
				continue
			}
			if dep == i {
				t.Errorf("subgoal %q depends on itself", sg.ID)
			}
			if _, dup := seen[dep]; dup {
				t.Errorf("subgoal %q has duplicate dependency %d", sg.ID, dep)
			}
			seen[dep] = struct{}{}
		}
	}

	edges := make([][]int, len(subgoals))
	for i, sg := range subgoals {
		for _, raw := range sg.Deps {
			dep, err := strconv.Atoi(raw)
			if err != nil || dep < 0 || dep >= len(subgoals) {
				continue
			}
			edges[dep] = append(edges[dep], i)
		}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, len(subgoals))
	var visit func(int) bool
	visit = func(i int) bool {
		switch color[i] {
		case gray:
			return false
		case black:
			return true
		}
		color[i] = gray
		for _, child := range edges[i] {
			if !visit(child) {
				return false
			}
		}
		color[i] = black
		return true
	}
	for i := range subgoals {
		if !visit(i) {
			t.Fatalf("subgoal dependency graph is cyclic near index %d", i)
		}
	}
}

func subgoalIndex(id string) (int, bool) {
	const prefix = "subgoal:"
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	if err != nil {
		return 0, false
	}
	return n, true
}
