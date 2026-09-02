package main

import (
	"context"
	"path/filepath"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/engine/rerank"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/sqlite"
)

type engineBundle struct {
	db         *sqlite.DB
	chat       *llm.Client
	embed      *llm.Client
	embedIndex *llm.Client
	vector     *sqlite.VectorStore
	graph      *sqlite.GraphStore
	history    *sqlite.HistoryStore
	bm25       bm25.Indexer
	indexer    *engine.Indexer
	updater    *graph.GraphUpdater
}

func newEngineBundle(env config.Env) (*engineBundle, error) {
	return newEngineBundleAt(env, filepath.Join(env.PersistDir, "kb.db"))
}

func newEngineBundleAt(env config.Env, dbPath string) (*engineBundle, error) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}

	chat := llm.NewClient(llm.Config{
		BaseURL:           env.LLMBaseURL,
		NoProxyHosts:      env.NoProxy,
		DefaultEmbedModel: env.EmbedModel,
		RequestTimeout:    env.LLMTimeout,
		MaxTokens:         env.LLMMaxTokens,
		NoThink:           env.LLMNoThink,
		RedactPII:         env.PIIRedact,
	})

	// Query-time embeddings (retrieval) use KB_EMBED_BASE_URL — pinned to the
	// local endpoint so a query never contends with the remote chat
	// model for hardware. Bulk indexing embeds use KB_EMBED_INDEX_BASE_URL,
	// which defaults to LLMBaseURL (the remote host), so the heavy
	// one-time work never runs on the local endpoint. The local instance
	// therefore only serves live query embeddings.
	queryEmbedURL := env.EmbedBaseURL
	if queryEmbedURL == "" {
		queryEmbedURL = config.DefaultLocalLLMURL
	}
	indexEmbedURL := env.EmbedIndexBaseURL
	if indexEmbedURL == "" {
		indexEmbedURL = env.LLMBaseURL
	}
	embed := llm.NewClient(llm.Config{
		BaseURL:           queryEmbedURL,
		NoProxyHosts:      env.NoProxy,
		DefaultEmbedModel: env.EmbedModel,
		RequestTimeout:    env.LLMTimeout,
	})
	embedIndex := llm.NewClient(llm.Config{
		BaseURL:           indexEmbedURL,
		NoProxyHosts:      env.NoProxy,
		DefaultEmbedModel: env.EmbedModel,
		RequestTimeout:    env.LLMTimeout,
	})

	vectorStore := sqlite.NewVectorStore(db)
	graphStore := sqlite.NewGraphStore(db)
	historyStore := sqlite.NewHistoryStore(db)
	var bm25idx bm25.Indexer
	if env.FTS5 {
		bm25idx = sqlite.NewFTS5Index(db)
	} else {
		bm25idx = bm25.New()
	}
	chatExtractor := graph.NewChatExtractor(chat, env.LLMModel)
	chatExtractor.Classify = true
	updater := graph.NewGraphUpdater(graphStore, graph.NewExtractor(chat, env.LLMModel), graph.NewSummarizer(chat, env.LLMModel)).
		WithExtractConcurrency(2).
		WithLegalExtractor(graph.NewLegalExtractor(chat, env.LLMModel)).
		WithChatExtractor(chatExtractor).
		WithCodeRoot(env.KBRoot).
		WithCommunityDetector(graph.NewCommunityDetector(env.CommunityAlgo))
	graphForIndex := updater
	if !env.IndexGraph {
		graphForIndex = nil
	}
	idx := engine.NewIndexer(engine.Config{
		Root:         env.KBRoot,
		Vector:       vectorStore,
		Graph:        graphForIndex,
		Embed:        embedIndex,
		EmbedModel:   env.EmbedModel,
		ChunkSize:    env.ChunkSize,
		ChunkOverlap: env.ChunkOverlap,
	})
	graphStore.RefreshFunc = updater.RefreshStaleCommunities

	return &engineBundle{
		db:         db,
		chat:       chat,
		embed:      embed,
		embedIndex: embedIndex,
		vector:     vectorStore,
		graph:      graphStore,
		history:    historyStore,
		bm25:       bm25idx,
		indexer:    idx,
		updater:    updater,
	}, nil
}

func (b *engineBundle) close() {
	b.db.Close()
}

func rerankFromEnv(env config.Env, chat *llm.Client) rerank.Reranker {
	switch env.Rerank {
	case "llm":
		return rerank.NewLLM(chat, env.LLMModel)
	case "onnx":
		return rerank.ONNX{}
	default:
		return rerank.Noop{}
	}
}
