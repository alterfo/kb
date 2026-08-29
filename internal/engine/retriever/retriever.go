package retriever

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

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
	RRFK           int
	PerDocCap      int
	DefaultK       int
	CandidateK     int
	GraphHops      int
	SetMaxRounds   int
	SupersedeMode  SupersedeMode
	IntraDocBudget int
	Clock          func() time.Time
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
			log.Printf("retriever: stale community count: %v (continuing)", err)
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
			log.Printf("retriever: refresh stale communities: %v (continuing)", err)
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
	k := opt.K
	if k <= 0 {
		k = r.cfg.DefaultK
	}
	r.refreshStaleCommunities(ctx)
	switch opt.Mode {
	case ModeGlobal:
		return r.retrieveGlobal(ctx, query, opt, k)
	case ModeDrift:
		return r.retrieveDrift(ctx, query, opt, k)
	case ModeSet:
		return r.retrieveSet(ctx, query, opt, k)
	default:
		return r.retrieveLocal(ctx, query, opt, k)
	}
}

func (r *Retriever) retrieveLocal(ctx context.Context, query string, opt Options, k int) ([]vector.ScoredChunk, error) {
	chunkByID := make(map[string]vector.Chunk)
	rankLists := r.localLegs(ctx, query, opt.Filter, chunkByID)
	if len(rankLists) == 0 {
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
	if list := r.denseRankLists(ctx, subqueries, filter, chunkByID); len(list) > 0 {
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

	scored := make([]vector.ScoredChunk, 0, len(fused))
	for id, chunk := range chunkByID {
		if _, ok := fused[id]; !ok {
			continue
		}
		if !opt.Filter.Matches(chunk.Source, chunk.Metadata) {
			continue
		}
		final := (normalized[id] + authorityBonus(chunk.FilePath, r.cfg.AuthorityBonus)) * supersededPenalty(chunk.SupersededBy)
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

func (r *Retriever) denseRankLists(ctx context.Context, subqueries []string, filter vector.Filter, chunkByID map[string]vector.Chunk) [][]string {
	if r.cfg.Embed == nil || r.cfg.Vector == nil || len(subqueries) == 0 {
		return nil
	}

	vecs, err := r.cfg.Embed.Embed(ctx, r.cfg.EmbedModel, subqueries)
	if err != nil {
		return nil
	}

	var lists [][]string
	for _, vec := range vecs {
		if len(vec) == 0 {
			continue
		}
		results, err := r.cfg.Vector.Query(ctx, vec, r.cfg.CandidateK, filter)
		if err != nil {
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
