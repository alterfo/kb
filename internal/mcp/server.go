// Package mcp exposes the knowledge base over the Model Context Protocol:
// search, ask, get_document, list_sources, add_note, add_source,
// graph_query, generate_report, reindex, status. It is the server side of
// MCP (internal/connectors/mcp is the client side, for ingesting from other
// MCP servers).
package mcp

import (
	"context"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/rerank"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/vector"
	"github.com/alterfo/kb/internal/verify"
)

// ChatClient runs a single chat completion. Satisfied by *llm.Client.
type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

// Embedder embeds texts. Satisfied by *llm.Client.
type Embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// CorpusVersioner reports the SQLite store's write-generation counter, used
// to lazily rebuild the in-memory BM25 index. Satisfied by *sqlite.DB.
type CorpusVersioner interface {
	CorpusVersion(ctx context.Context) (int, error)
}

// Deps wires the MCP server's dependencies and tunables. Chat, Embed,
// Reranker and Graph are optional: every tool built on top of them degrades
// fail-open, mirroring the rest of the engine.
type Deps struct {
	Root       string
	Vector     vector.Store
	Versioner  CorpusVersioner
	BM25       bm25.Indexer
	Graph      graphstore.Store
	Indexer    *engine.Indexer
	Chat       ChatClient
	Embed      Embedder
	Reranker   rerank.Reranker
	LLMModel   string
	EmbedModel string

	Hybrid               bool
	AuthorityBonus       map[string]float64
	RRFK                 int
	DefaultK             int
	DetectContradictions bool
	RollingMemory        int
	CandidateK           int
	PerDocCap            int
	SetMaxRounds         int
	IntraDocBudget       int
	AbstainThreshold     float64
	SupersedeMode        string
	QualifierFilter      bool
	ANNPrefilter         bool
	AskCache             got.AskCache

	SourcesPath string
}

// Server holds the wired tool implementations and the underlying SDK server.
type Server struct {
	deps      Deps
	retriever *retriever.Retriever
	orch      *got.Orchestrator
	sdk       *sdk.Server
}

// NewServer builds every tool against deps and registers them on a fresh
// SDK server.
func NewServer(deps Deps) *Server {
	r := retriever.New(retriever.Config{
		Vector:         deps.Vector,
		BM25:           deps.BM25,
		Chat:           deps.Chat,
		Embed:          deps.Embed,
		Reranker:       deps.Reranker,
		Graph:          deps.Graph,
		LLMModel:       deps.LLMModel,
		EmbedModel:     deps.EmbedModel,
		Hybrid:         deps.Hybrid,
		AuthorityBonus: deps.AuthorityBonus,
		RRFK:           deps.RRFK,
		DefaultK:       deps.DefaultK,
		CandidateK:     deps.CandidateK,
		PerDocCap:      deps.PerDocCap,
		SetMaxRounds:   deps.SetMaxRounds,
		IntraDocBudget: deps.IntraDocBudget,
		SupersedeMode:  retriever.SupersedeMode(deps.SupersedeMode),
		ANNPrefilter:   deps.ANNPrefilter,
	})

	orch := got.New(got.Config{
		Retriever:             retriever.Adapter{Retriever: r},
		Chat:                  deps.Chat,
		Model:                 deps.LLMModel,
		ContradictionDetector: verify.NewContradictionDetector(deps.Chat, deps.LLMModel),
		DetectContradictions:  deps.DetectContradictions,
		ExtractQualifiers:     deps.QualifierFilter,
		AbstainThreshold:      deps.AbstainThreshold,
		RollingMemory:         deps.RollingMemory,
		AskCache:              deps.AskCache,
	})

	s := &Server{
		deps:      deps,
		retriever: r,
		orch:      orch,
		sdk:       sdk.NewServer(&sdk.Implementation{Name: "kb", Version: "0.1.0"}, nil),
	}
	s.registerTools()
	return s
}

// Run serves the MCP protocol over t until the connection closes or ctx is
// canceled.
func (s *Server) Run(ctx context.Context, t sdk.Transport) error {
	return s.sdk.Run(ctx, t)
}

// HTTPHandler serves the MCP protocol over the SDK's Streamable HTTP
// transport, so the same tool set that `kb mcp` exposes over stdio can also
// be mounted as a route inside `kb serve` (see cmd/kb/serve.go) for remote
// MCP clients.
func (s *Server) HTTPHandler() http.Handler {
	return sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		return s.sdk
	}, nil)
}

// ToolInfo describes a registered tool for display purposes (the
// dashboard's /mcp/info page). Kept in sync with registerTools below.
type ToolInfo struct {
	Name        string
	Description string
}

func (s *Server) Tools() []ToolInfo {
	return []ToolInfo{
		{"search", "Hybrid + graph-aware retrieval over the knowledge base; returns ranked chunks."},
		{"ask", "Graph-of-Thoughts: decompose, retrieve, synthesize, and refine a full answer with sources."},
		{"get_document", "Read a document's raw content and frontmatter by its KB_ROOT-relative path."},
		{"list_sources", "List configured source instances (presence-only, no secret values) and virtual collections."},
		{"add_note", "Write a new markdown note under KB_ROOT and index it."},
		{"add_source", "Append a new source instance to sources.yaml (secrets are env-var names, never literal values)."},
		{"graph_query", "Match an entity by name and return its neighbors and communities."},
		{"generate_report", "Generate a grounded search-synthesis answer or a GraphRAG global community report."},
		{"reindex", "Reindex a single path (file or directory) or the whole KB_ROOT tree."},
		{"status", "Report corpus version, chunk count, and knowledge-graph size."},
	}
}

func (s *Server) registerTools() {
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "search",
		Description: "Hybrid + graph-aware retrieval over the knowledge base; returns ranked chunks.",
	}, s.search)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "ask",
		Description: "Graph-of-Thoughts: decompose, retrieve, synthesize, and refine a full answer with sources.",
	}, s.ask)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "get_document",
		Description: "Read a document's raw content and frontmatter by its KB_ROOT-relative path.",
	}, s.getDocument)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "list_sources",
		Description: "List configured source instances (presence-only, no secret values) and virtual collections.",
	}, s.listSources)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "add_note",
		Description: "Write a new markdown note under KB_ROOT and index it.",
	}, s.addNote)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "add_source",
		Description: "Append a new source instance to sources.yaml (secrets are env-var names, never literal values).",
	}, s.addSource)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "graph_query",
		Description: "Match an entity by name and return its neighbors and communities.",
	}, s.graphQuery)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "generate_report",
		Description: "Generate a grounded search-synthesis answer or a GraphRAG global community report.",
	}, s.generateReport)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "reindex",
		Description: "Reindex a single path (file or directory) or the whole KB_ROOT tree.",
	}, s.reindex)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "status",
		Description: "Report corpus version, chunk count, and knowledge-graph size.",
	}, s.status)
}

func (s *Server) refreshBM25(ctx context.Context) {
	if s.deps.BM25 == nil || s.deps.Versioner == nil || s.deps.Vector == nil {
		return
	}
	_ = s.deps.BM25.Refresh(ctx, s.deps.Versioner, s.deps.Vector)
}
