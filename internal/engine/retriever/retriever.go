package retriever

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/alterfo/kb/internal/engine/metrics"
	"github.com/alterfo/kb/internal/engine/rerank"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/vector"
)

const (
	staleCommunityRefreshThreshold = 1
	staleCommunityMinInterval      = 30 * time.Second
)

// Embedder embeds query texts. Satisfied by *llm.Client.
type Embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// BM25Searcher is the lexical side of the hybrid retriever. Satisfied by *bm25.Index.
type BM25Searcher interface {
	Search(query string, k int) []bm25.ScoredID
	Chunk(id string) (vector.Chunk, bool)
}

// Mode selects the retrieval pipeline for a query. The zero value is
// ModeLocal, which keeps existing callers on the hybrid pipeline.
type Mode string

const (
	ModeLocal  Mode = "local"
	ModeGlobal Mode = "global"
	ModeDrift  Mode = "drift"
)

// Options controls a single Retrieve call.
type Options struct {
	K      int
	Filter vector.Filter
	Mode   Mode
	// RelevantIDs, when non-empty, enables recall@k in Result.Metrics.
	RelevantIDs []string
}

// Result is a retrieval outcome with observability metadata. It exists so
// fail-open degradation and per-run metrics can be returned without
// changing the existing chunks+error API.
type Result struct {
	Chunks   []vector.ScoredChunk `json:"chunks"`
	Metrics  metrics.Values       `json:"metrics"`
	Degraded []string             `json:"degraded,omitempty"`
}

type degradedContextKey struct{}

func withDegradedCollector(ctx context.Context, degraded *[]string) context.Context {
	return context.WithValue(ctx, degradedContextKey{}, degraded)
}

func addDegraded(ctx context.Context, msg string) {
	degraded, ok := ctx.Value(degradedContextKey{}).(*[]string)
	if ok && degraded != nil {
		*degraded = append(*degraded, msg)
	}
}

type costContextKey struct{}

func withCostCollector(ctx context.Context, cost *metrics.Cost) context.Context {
	return context.WithValue(ctx, costContextKey{}, cost)
}

func addCost(ctx context.Context, cost metrics.Cost) {
	total, ok := ctx.Value(costContextKey{}).(*metrics.Cost)
	if ok && total != nil {
		total.Add(cost)
	}
}

// Config wires the retriever's dependencies and tunables. Every dependency
// is optional except Vector: a nil Chat disables query expansion, a nil
// Embed or nil BM25 disables the corresponding retrieval leg, all fail-open.
type Config struct {
	Vector         vector.Store
	BM25           BM25Searcher
	Chat           ChatClient
	Embed          Embedder
	Reranker       rerank.Reranker
	Graph          GraphStore
	LLMModel       string
	EmbedModel     string
	Hybrid         bool
	AuthorityBonus map[string]float64
	Feedback       FeedbackPrior
	FeedbackBonus  float64
	RRFK           int
	PerDocCap      int
	DefaultK       int
	CandidateK     int
	GraphHops      int
	SetMaxRounds   int
	SupersedeMode  SupersedeMode
	IntraDocBudget int
	ANNPrefilter   bool
	Clock          func() time.Time
}

// candidateVectorStore is the optional vector-store capability used by the
// ANN prefilter. *sqlite.VectorStore implements it; stores without it keep
// the exhaustive Query path.
type candidateVectorStore interface {
	QueryCandidates(ctx context.Context, vec []float32, k int, candidateIDs []string, filter vector.Filter) ([]vector.ScoredChunk, error)
}

type Retriever struct {
	cfg                  Config
	mu                   sync.Mutex
	lastCommunityRefresh time.Time
}

func New(cfg Config) *Retriever {
	if cfg.RRFK <= 0 {
		cfg.RRFK = 60
	}
	if cfg.PerDocCap <= 0 {
		cfg.PerDocCap = 2
	}
	if cfg.DefaultK <= 0 {
		cfg.DefaultK = 10
	}
	if cfg.CandidateK <= 0 {
		cfg.CandidateK = 20
	}
	if cfg.GraphHops <= 0 {
		cfg.GraphHops = 2
	}
	if cfg.SetMaxRounds <= 0 {
		cfg.SetMaxRounds = 3
	}
	if cfg.SupersedeMode != SupersedeStrict {
		cfg.SupersedeMode = SupersedeSoft
	}
	return &Retriever{cfg: cfg}
}

// refreshStaleCommunities is the lazy-communities pre-query step: when the
// graph has stale communities above the threshold and the minimum interval
// since the last refresh has elapsed, batch-refresh them before the query
// runs. Fail-open: any error (count or refresh) leaves the stale summaries
// in place and the query proceeds untouched.
func (r *Retriever) refreshStaleCommunities(ctx context.Context) {
	if r.cfg.Graph == nil {
		return
	}
	r.mu.Lock()
	now := time.Now
	if r.cfg.Clock != nil {
		now = r.cfg.Clock
	}
	elapsed := now().Sub(r.lastCommunityRefresh)
	if r.lastCommunityRefresh.IsZero() || elapsed >= staleCommunityMinInterval {
		n, err := r.cfg.Graph.StaleCommunityCount(ctx)
		if err != nil {
			slog.Error("stale community count failed; continuing", "error", err)
			addDegraded(ctx, "stale community count unavailable: "+err.Error())
			r.mu.Unlock()
			return
		}
		if n < staleCommunityRefreshThreshold {
			r.lastCommunityRefresh = now()
			r.mu.Unlock()
			return
		}
		r.lastCommunityRefresh = now()
		r.mu.Unlock()
		if _, err := r.cfg.Graph.RefreshStaleCommunities(ctx); err != nil {
			slog.Error("refresh stale communities failed; continuing", "error", err)
			addDegraded(ctx, "stale community refresh unavailable: "+err.Error())
		}
		return
	}
	r.mu.Unlock()
}

// Retrieve dispatches to the pipeline selected by opt.Mode. ModeLocal runs
// the full hybrid + graph-aware pipeline: LLM query expansion (fail-open),
// dense multi-query search + BM25 search + graph-neighbor chunks +
// community summaries, all fused via RRF, then authority-prior re-scoring
// and a per-document coverage cap. ModeGlobal runs map-reduce over root
// community summaries; ModeDrift seeds a local search from the community
// summaries closest to the query. Every stage degrades to a partial result
// on failure; Retrieve only returns an error for cases outside retrieval
// itself (currently: never).
func (r *Retriever) Retrieve(ctx context.Context, query string, opt Options) ([]vector.ScoredChunk, error) {
	chunks, _, err := r.retrieve(ctx, query, opt)
	return chunks, err
}

// RetrieveWithResult is Retrieve plus the observability metadata needed by
// the MCP/web response contract: latency, optional recall@k, estimated
// retrieval cost, and any fail-open degradations encountered during the
// call.
func (r *Retriever) RetrieveWithResult(ctx context.Context, query string, opt Options) Result {
	var degraded []string
	var cost metrics.Cost
	ctx = withDegradedCollector(ctx, &degraded)
	ctx = withCostCollector(ctx, &cost)

	start := time.Now()
	chunks, _, err := r.retrieve(ctx, query, opt)
	res := Result{Chunks: chunks, Degraded: degraded}
	res.Metrics.LatencyMS = metrics.LatencyMS(start)
	res.Metrics.Cost = cost
	if err != nil {
		res.Degraded = append(res.Degraded, "retrieval error: "+err.Error())
	}
	if len(opt.RelevantIDs) > 0 {
		k := opt.K
		if k <= 0 {
			k = r.cfg.DefaultK
		}
		res.Metrics.RecallAtK = metrics.ComputeRecallAtK(chunks, metrics.RelevantSet(opt.RelevantIDs), k)
	}
	return res
}

// retrieve is the shared dispatch path for Retrieve and RetrieveWithResult.
func (r *Retriever) retrieve(ctx context.Context, query string, opt Options) ([]vector.ScoredChunk, []string, error) {
	k := opt.K
	if k <= 0 {
		k = r.cfg.DefaultK
	}
	r.refreshStaleCommunities(ctx)
	switch opt.Mode {
	case ModeGlobal:
		chunks, err := r.retrieveGlobal(ctx, query, opt, k)
		return chunks, nil, err
	case ModeDrift:
		chunks, err := r.retrieveDrift(ctx, query, opt, k)
		return chunks, nil, err
	case ModeSet:
		chunks, err := r.retrieveSet(ctx, query, opt, k)
		return chunks, nil, err
	default:
		chunks, err := r.retrieveLocal(ctx, query, opt, k)
		return chunks, nil, err
	}
}

func (r *Retriever) retrieveLocal(ctx context.Context, query string, opt Options, k int) ([]vector.ScoredChunk, error) {
	chunkByID := make(map[string]vector.Chunk)
	rankLists := r.localLegs(ctx, query, opt.Filter, chunkByID)
	if len(rankLists) == 0 {
		addDegraded(ctx, "all retrieval legs unavailable for local query")
		return nil, nil
	}
	scored := r.fuseRankLists(ctx, query, opt, k, chunkByID, rankLists)
	return r.expandIntraDoc(ctx, scored, k, opt.Filter), nil
}

// localLegs collects the hybrid + graph-aware rank lists of the local
// pipeline into a shared chunk registry, so mode-specific legs can add
// their own lists/chunks before fusion.
func (r *Retriever) localLegs(ctx context.Context, query string, filter vector.Filter, chunkByID map[string]vector.Chunk) [][]string {
	var rankLists [][]string

	subqueries := expandQuery(ctx, r.cfg.Chat, r.cfg.LLMModel, query)
	if list := r.denseRankLists(ctx, query, subqueries, filter, chunkByID); len(list) > 0 {
		rankLists = append(rankLists, list...)
	}

	if r.cfg.Hybrid && r.cfg.BM25 != nil {
		if ids := r.bm25RankList(query, filter, chunkByID); len(ids) > 0 {
			rankLists = append(rankLists, ids)
		}
	}

	if lists := r.graphRankLists(ctx, query, filter, chunkByID); len(lists) > 0 {
		rankLists = append(rankLists, lists...)
	}
	return rankLists
}

// fuseRankLists applies RRF fusion, authority-prior re-scoring, reranking,
// and the per-document coverage cap to the given rank lists.
func (r *Retriever) fuseRankLists(ctx context.Context, query string, opt Options, k int, chunkByID map[string]vector.Chunk, rankLists [][]string) []vector.ScoredChunk {
	fused := rrfScores(rankLists, r.cfg.RRFK)
	normalized := minMaxNormalize(fused)
	prior := r.personalPrior(ctx)

	scored := make([]vector.ScoredChunk, 0, len(fused))
	for id, chunk := range chunkByID {
		if _, ok := fused[id]; !ok {
			continue
		}
		if !opt.Filter.Matches(chunk.Source, chunk.Metadata) {
			continue
		}
		final := (normalized[id] + authorityBonus(chunk.FilePath, r.cfg.AuthorityBonus) + feedbackPrior(prior, r.cfg.FeedbackBonus, chunk.RefDocID)) * supersededPenalty(chunk.SupersededBy)
		scored = append(scored, vector.ScoredChunk{Chunk: chunk, Score: final})
	}

	if r.cfg.SupersedeMode == SupersedeStrict {
		scored = dropSupersededWithNewerPresent(scored)
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Chunk.ID < scored[j].Chunk.ID
	})

	scored = r.rerank(ctx, query, scored)

	return capPerDoc(scored, r.cfg.PerDocCap, k)
}

type Adapter struct{ *Retriever }

func (a Adapter) Retrieve(ctx context.Context, query string, k int) ([]vector.ScoredChunk, error) {
	return a.Retriever.Retrieve(ctx, query, Options{K: k})
}

// RetrieveMode exposes mode-aware retrieval to orchestrators that select a
// mode per sub-query.
func (a Adapter) RetrieveMode(ctx context.Context, query string, k int, mode Mode) ([]vector.ScoredChunk, error) {
	return a.Retriever.Retrieve(ctx, query, Options{K: k, Mode: mode})
}

// RetrieveModeFiltered is RetrieveMode with an explicit structured filter;
// orchestrators that extract query qualifiers use it to constrain every leg.
func (a Adapter) RetrieveModeFiltered(ctx context.Context, query string, k int, mode Mode, filter vector.Filter) ([]vector.ScoredChunk, error) {
	return a.Retriever.Retrieve(ctx, query, Options{K: k, Mode: mode, Filter: filter})
}

// rerank applies the configured Reranker, fail-open: any error or a
// length mismatch (a misbehaving implementation) leaves scored untouched.
func (r *Retriever) rerank(ctx context.Context, query string, scored []vector.ScoredChunk) []vector.ScoredChunk {
	if r.cfg.Reranker == nil {
		return scored
	}
	reranked, err := r.cfg.Reranker.Rerank(ctx, query, scored)
	if err != nil || len(reranked) != len(scored) {
		return scored
	}
	return reranked
}

func (r *Retriever) denseRankLists(ctx context.Context, query string, subqueries []string, filter vector.Filter, chunkByID map[string]vector.Chunk) [][]string {
	if r.cfg.Embed == nil || r.cfg.Vector == nil || len(subqueries) == 0 {
		return nil
	}

	vecs, err := r.cfg.Embed.Embed(ctx, r.cfg.EmbedModel, subqueries)
	if err != nil {
		addDegraded(ctx, "dense retrieval unavailable: "+err.Error())
		return nil
	}

	var lists [][]string
	for i, vec := range vecs {
		if len(vec) == 0 {
			continue
		}
		subquery := query
		if i < len(subqueries) {
			subquery = subqueries[i]
		}
		results, err := r.queryDense(ctx, vec, subquery, filter)
		if err != nil {
			addDegraded(ctx, "dense vector query failed: "+err.Error())
			continue
		}
		ids := make([]string, 0, len(results))
		for _, sc := range results {
			chunkByID[sc.Chunk.ID] = sc.Chunk
			ids = append(ids, sc.Chunk.ID)
		}
		if len(ids) > 0 {
			lists = append(lists, ids)
		}
	}
	return lists
}

// queryDense scores one query vector against the corpus, using the ANN
// prefilter candidate set when enabled. It falls back to exhaustive Query
// on an empty candidate set, an unsupported store, or a prefilter error so
// enabling the flag never removes the dense retrieval leg.
func (r *Retriever) queryDense(ctx context.Context, vec []float32, query string, filter vector.Filter) ([]vector.ScoredChunk, error) {
	if !r.cfg.ANNPrefilter {
		return r.cfg.Vector.Query(ctx, vec, r.cfg.CandidateK, filter)
	}

	candidates := r.prefilterCandidates(ctx, query)
	if len(candidates) == 0 {
		addDegraded(ctx, "ANN prefilter produced no candidates; falling back to exhaustive vector search")
		return r.cfg.Vector.Query(ctx, vec, r.cfg.CandidateK, filter)
	}
	store, ok := r.cfg.Vector.(candidateVectorStore)
	if !ok {
		addDegraded(ctx, "ANN prefilter requested but vector store lacks candidate query support; falling back to exhaustive vector search")
		return r.cfg.Vector.Query(ctx, vec, r.cfg.CandidateK, filter)
	}
	results, err := store.QueryCandidates(ctx, vec, r.cfg.CandidateK, candidates, filter)
	if err != nil {
		addDegraded(ctx, "ANN prefilter query failed; falling back to exhaustive vector search: "+err.Error())
		return r.cfg.Vector.Query(ctx, vec, r.cfg.CandidateK, filter)
	}
	if len(results) == 0 {
		// Metadata/source filters may exclude every prefiltered candidate
		// even though the corpus has matching chunks outside the candidate
		// set. Fall back rather than silently returning an empty dense leg.
		addDegraded(ctx, "ANN prefilter candidates matched nothing after filtering; falling back to exhaustive vector search")
		return r.cfg.Vector.Query(ctx, vec, r.cfg.CandidateK, filter)
	}
	return results, nil
}

// prefilterCandidates builds the small dense-retrieval candidate set from
// FTS5 lexical hits plus source chunks of graph entities linked from the
// query. It is capped to keep cosine scoring O(K) rather than O(N).
func (r *Retriever) prefilterCandidates(ctx context.Context, query string) []string {
	limit := r.cfg.CandidateK * 4
	if limit < 64 {
		limit = 64
	}

	seen := make(map[string]struct{})
	var out []string
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		if len(out) >= limit {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	if r.cfg.BM25 != nil {
		for _, hit := range r.cfg.BM25.Search(query, r.cfg.CandidateK) {
			add(hit.ID)
		}
	}
	if r.cfg.Graph != nil {
		for _, e := range linkEntities(ctx, r.cfg.Graph, query) {
			for _, chunkID := range e.SourceChunks {
				add(chunkID)
			}
		}
	}
	return out
}

func (r *Retriever) bm25RankList(query string, filter vector.Filter, chunkByID map[string]vector.Chunk) []string {
	results := r.cfg.BM25.Search(query, r.cfg.CandidateK)
	ids := make([]string, 0, len(results))
	for _, res := range results {
		chunk, ok := r.cfg.BM25.Chunk(res.ID)
		if !ok {
			continue
		}
		if !filter.Matches(chunk.Source, chunk.Metadata) {
			continue
		}
		chunkByID[chunk.ID] = chunk
		ids = append(ids, chunk.ID)
	}
	return ids
}

func capPerDoc(scored []vector.ScoredChunk, perDocCap, k int) []vector.ScoredChunk {
	capped := make([]vector.ScoredChunk, 0, len(scored))
	perDoc := make(map[string]int)
	for _, sc := range scored {
		if perDoc[sc.Chunk.RefDocID] >= perDocCap {
			continue
		}
		perDoc[sc.Chunk.RefDocID]++
		capped = append(capped, sc)
		if len(capped) >= k {
			break
		}
	}
	return capped
}
