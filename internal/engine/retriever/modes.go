package retriever

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
)

const (
	maxGlobalCommunities = 20
	maxDriftCommunities  = 3
	maxDriftSeedChunks   = 30
	globalPartialWorkers = 4
)

const globalPartialSystemPrompt = `You are answering a user question with the help of a knowledge graph. ` +
	`You are given ONE community summary. State briefly what this community contributes to the question. ` +
	`If it contributes nothing relevant, reply with exactly: nothing relevant. No markdown fences.`

const globalReduceSystemPrompt = `You synthesize a final answer to a user question from partial answers ` +
	`produced by different communities of a knowledge graph. Ground every claim in the partial answers. ` +
	`If no partial answer is relevant, say so. No markdown fences.`

// retrieveGlobal runs map-reduce over root-level community summaries: one
// partial answer per community, then a single reduce step, returned as a
// synthetic answer chunk plus the community summaries as evidence. Fail-
// open: a nil Graph/Chat or no summarized communities degrades to the
// local pipeline.
func (r *Retriever) retrieveGlobal(ctx context.Context, query string, opt Options, k int) ([]vector.ScoredChunk, error) {
	if !isEmptyFilter(opt.Filter) {
		addDegraded(ctx, "global mode is incompatible with filters; falling back to local retrieval")
		return r.retrieveLocal(ctx, query, opt, k)
	}
	if r.cfg.Graph == nil || r.cfg.Chat == nil {
		addDegraded(ctx, "global mode requires graph and chat; falling back to local retrieval")
		return r.retrieveLocal(ctx, query, opt, k)
	}
	roots := r.rootCommunities(ctx)
	if len(roots) == 0 {
		addDegraded(ctx, "global mode found no summarized communities; falling back to local retrieval")
		return r.retrieveLocal(ctx, query, opt, k)
	}

	partials := r.partialAnswers(ctx, query, roots)
	reduced := r.reduceAnswers(ctx, query, partials)
	if strings.TrimSpace(reduced) == "" {
		addDegraded(ctx, "global reduce returned no answer; returning community summaries")
		return communitySummaryChunks(roots, k), nil
	}

	chunks := []vector.ScoredChunk{{
		Chunk: vector.Chunk{
			ID:       "global:answer",
			RefDocID: "global:answer",
			Text:     reduced,
			FilePath: "graph/global",
			FileName: "global synthesis",
			Source:   "graph",
		},
		Score: 1,
	}}
	return append(chunks, communitySummaryChunks(roots, k)...), nil
}

// rootCommunities returns the coarsest level of the community hierarchy
// (the level with the largest number), keeping only communities with
// summaries, deterministically ordered and capped.
func (r *Retriever) rootCommunities(ctx context.Context) []graphstore.Community {
	all, err := r.cfg.Graph.AllCommunities(ctx)
	if err != nil {
		return nil
	}
	maxLevel := -1
	for _, c := range all {
		if c.Level > maxLevel {
			maxLevel = c.Level
		}
	}
	var roots []graphstore.Community
	for _, c := range all {
		if c.Level == maxLevel && strings.TrimSpace(c.Summary) != "" {
			roots = append(roots, c)
		}
	}
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].ID < roots[j].ID })
	if len(roots) > maxGlobalCommunities {
		roots = roots[:maxGlobalCommunities]
	}
	return roots
}

// partialAnswers asks one partial-answer question per community summary,
// across a bounded worker pool, fail-open to "nothing relevant" on any chat
// error or empty response. Results keep community order so the reduce step
// sees a deterministic input.
func (r *Retriever) partialAnswers(ctx context.Context, query string, communities []graphstore.Community) []string {
	out := make([]string, len(communities))
	sem := make(chan struct{}, globalPartialWorkers)
	var wg sync.WaitGroup
	for i, c := range communities {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c graphstore.Community) {
			defer wg.Done()
			defer func() { <-sem }()
			resp, err := r.cfg.Chat.Chat(ctx, llm.ChatRequest{
				Model: r.cfg.LLMModel,
				Messages: []llm.ChatMessage{
					{Role: "system", Content: globalPartialSystemPrompt},
					{Role: "user", Content: "Question: " + query + "\n\nCommunity summary:\n" + c.Summary},
				},
			})
			if err != nil || strings.TrimSpace(resp.Content) == "" {
				out[i] = "nothing relevant"
				return
			}
			out[i] = resp.Content
		}(i, c)
	}
	wg.Wait()
	return out
}

// reduceAnswers asks the chat model to merge partial answers into one final
// answer. Fail-open to "" on error, which makes retrieveGlobal return just
// the community summaries.
func (r *Retriever) reduceAnswers(ctx context.Context, query string, partials []string) string {
	var b strings.Builder
	for _, p := range partials {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	resp, err := r.cfg.Chat.Chat(ctx, llm.ChatRequest{
		Model: r.cfg.LLMModel,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: globalReduceSystemPrompt},
			{Role: "user", Content: "Question: " + query + "\n\nPartial answers:\n" + b.String()},
		},
	})
	if err != nil {
		return ""
	}
	return resp.Content
}

// communitySummaryChunks wraps root community summaries as synthetic chunks
// with deterministic descending scores.
func communitySummaryChunks(communities []graphstore.Community, k int) []vector.ScoredChunk {
	sorted := append([]graphstore.Community(nil), communities...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	out := make([]vector.ScoredChunk, 0, len(sorted))
	score := 0.9
	for _, c := range sorted {
		if len(out) >= k {
			break
		}
		out = append(out, vector.ScoredChunk{Chunk: communityChunk(c), Score: score})
		score -= 0.05
	}
	return out
}

// retrieveDrift seeds retrieval with a vector search over community
// summaries, then refines with the full local pipeline fused in. Fail-open:
// a nil Graph or Embed, or no summarized communities, degrades to local.
func (r *Retriever) retrieveDrift(ctx context.Context, query string, opt Options, k int) ([]vector.ScoredChunk, error) {
	if r.cfg.Graph == nil || r.cfg.Embed == nil {
		addDegraded(ctx, "drift mode requires graph and embeddings; falling back to local retrieval")
		return r.retrieveLocal(ctx, query, opt, k)
	}
	chunkByID := make(map[string]vector.Chunk)
	var rankLists [][]string
	if list := r.driftSeedRankList(ctx, query, chunkByID); len(list) > 0 {
		rankLists = append(rankLists, list)
	}
	rankLists = append(rankLists, r.localLegs(ctx, query, opt.Filter, chunkByID)...)
	if len(rankLists) == 0 {
		return nil, nil
	}
	return r.fuseRankLists(ctx, query, opt, k, chunkByID, rankLists), nil
}

// driftSeedRankList embeds every community summary, keeps the top-k closest
// to the query, and returns a rank list of their summary chunks followed by
// their member source chunks (resolved through BM25).
func (r *Retriever) driftSeedRankList(ctx context.Context, query string, chunkByID map[string]vector.Chunk) []string {
	all, err := r.cfg.Graph.AllCommunities(ctx)
	if err != nil {
		return nil
	}
	var summarized []graphstore.Community
	for _, c := range all {
		if strings.TrimSpace(c.Summary) != "" {
			summarized = append(summarized, c)
		}
	}
	if len(summarized) == 0 {
		return nil
	}

	texts := make([]string, 0, len(summarized)+1)
	texts = append(texts, query)
	for _, c := range summarized {
		texts = append(texts, c.Summary)
	}
	vecs, err := r.cfg.Embed.Embed(ctx, r.cfg.EmbedModel, texts)
	if err != nil || len(vecs) != len(texts) {
		return nil
	}

	type scoredCommunity struct {
		c   graphstore.Community
		sim float64
	}
	scored := make([]scoredCommunity, 0, len(summarized))
	for i, c := range summarized {
		sim := cosineSim(vecs[0], vecs[i+1])
		if sim > 0 {
			scored = append(scored, scoredCommunity{c: c, sim: sim})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].sim != scored[j].sim {
			return scored[i].sim > scored[j].sim
		}
		return scored[i].c.ID < scored[j].c.ID
	})
	if len(scored) > maxDriftCommunities {
		scored = scored[:maxDriftCommunities]
	}

	var ids []string
	seen := make(map[string]struct{})
	for _, sc := range scored {
		chunk := communityChunk(sc.c)
		chunkByID[chunk.ID] = chunk
		ids = append(ids, chunk.ID)
		seen[chunk.ID] = struct{}{}

		for _, cid := range sc.c.SourceChunks {
			if _, ok := seen[cid]; ok {
				continue
			}
			if r.cfg.BM25 == nil {
				continue
			}
			chunk, ok := r.cfg.BM25.Chunk(cid)
			if !ok {
				continue
			}
			seen[cid] = struct{}{}
			chunkByID[chunk.ID] = chunk
			ids = append(ids, chunk.ID)
			if len(ids) >= maxDriftSeedChunks {
				return ids
			}
		}
	}
	return ids
}

func cosineSim(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}
