package web

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alterfo/kb/internal/engine"
	"github.com/alterfo/kb/internal/engine/got"
	"github.com/alterfo/kb/internal/engine/rerank"
	"github.com/alterfo/kb/internal/engine/retriever"
	"github.com/alterfo/kb/internal/governance"
	"github.com/alterfo/kb/internal/graph"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/mcp"
	"github.com/alterfo/kb/internal/store/bm25"
	"github.com/alterfo/kb/internal/store/graphstore"
	"github.com/alterfo/kb/internal/store/history"
	"github.com/alterfo/kb/internal/store/vector"
)

const defaultStaleAfter = 24 * time.Hour

type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
}

type Embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

type CorpusVersioner interface {
	CorpusVersion(ctx context.Context) (int, error)
}

type Deps struct {
	Root       string
	PersistDir string
	BaseCtx    context.Context

	Vector       vector.Store
	Versioner    CorpusVersioner
	BM25         bm25.Indexer
	Graph        graphstore.Store
	GraphUpdater *graph.GraphUpdater
	Indexer      *engine.Indexer
	History      history.Store
	MCP          *mcp.Server

	Chat                 ChatClient
	Embed                Embedder
	Reranker             rerank.Reranker
	LLMModel             string
	EmbedModel           string
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
	StatePath   string
	StaleAfter  time.Duration
	Now         func() time.Time
	EnvLookup   func(key string) (string, bool)
	Spawn       func(func())

	Governance *governance.Governance
}

type Server struct {
	deps    Deps
	baseCtx context.Context

	retriever *retriever.Retriever
	tmpl      *template.Template
	tmplErr   error
	asks      *askManager
	asksSem   chan struct{}
}

func NewServer(deps Deps) *Server {
	if deps.Root == "" {
		deps.Root = "./kb_root"
	}
	if deps.PersistDir == "" {
		deps.PersistDir = filepath.Join(deps.Root, ".persist")
	}
	if deps.SourcesPath == "" {
		deps.SourcesPath = filepath.Join(deps.Root, "sources.yaml")
	}
	if deps.StatePath == "" {
		deps.StatePath = filepath.Join(deps.PersistDir, ".sync-state.json")
	}
	if deps.StaleAfter <= 0 {
		deps.StaleAfter = defaultStaleAfter
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.EnvLookup == nil {
		deps.EnvLookup = os.LookupEnv
	}
	if deps.Spawn == nil {
		deps.Spawn = func(f func()) { go f() }
	}
	if deps.BaseCtx == nil {
		deps.BaseCtx = context.Background()
	}

	if deps.History != nil {
		// A run still "running" in the history table when the server starts
		// belongs to a goroutine that died with the previous process and can
		// never finish; leaving it "running" would show a permanently stuck
		// entry on the history page.
		_, _ = deps.History.MarkRunningInterrupted(deps.BaseCtx)
	}

	tmpl, err := parseTemplates()
	s := &Server{
		deps:    deps,
		baseCtx: deps.BaseCtx,
		tmpl:    tmpl,
		tmplErr: err,
		asks:    newAskManager(deps.Spawn),
		asksSem: make(chan struct{}, maxConcurrentAsks),
	}
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
	s.retriever = r
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /ask", s.handleAskPage)
	mux.HandleFunc("GET /ask/history", s.handleAskHistory)
	mux.HandleFunc("POST /ask/start", s.handleAskStart)
	mux.HandleFunc("GET /ask/status", s.handleAskStatus)
	mux.HandleFunc("GET /ask/events", s.handleAskEvents)
	mux.HandleFunc("POST /ask/approve", s.handleAskApprove)
	mux.HandleFunc("POST /ask/promote", s.handleAskPromote)
	mux.HandleFunc("GET /documents", s.handleDocuments)
	mux.HandleFunc("GET /documents/view", s.handleDocumentView)
	mux.HandleFunc("DELETE /documents", s.handleDocumentDelete)
	mux.HandleFunc("GET /documents/edit", s.handleDocumentEditForm)
	mux.HandleFunc("POST /documents/edit", s.handleDocumentEdit)
	mux.HandleFunc("POST /documents", s.handleAPIIngest)
	mux.HandleFunc("POST /documents/prune", s.handleAPIPrune)
	mux.HandleFunc("POST /documents/tombstone", s.handleAPITombstone)
	mux.HandleFunc("GET /add", s.handleAddForm)
	mux.HandleFunc("POST /add", s.handleAdd)
	mux.HandleFunc("GET /integrations", s.handleIntegrations)
	mux.HandleFunc("POST /integrations/save", s.handleIntegrationSave)
	mux.HandleFunc("POST /integrations/delete", s.handleIntegrationDelete)
	mux.HandleFunc("GET /reports", s.handleReportsForm)
	mux.HandleFunc("POST /reports", s.handleReports)
	mux.HandleFunc("GET /reports/node/estimate", s.handleNodeReportEstimate)
	mux.HandleFunc("GET /cleanup", s.handleCleanup)
	mux.HandleFunc("POST /cleanup", s.handleCleanupApply)
	mux.HandleFunc("POST /cleanup/rewrite", s.handleCleanupRewrite)
	mux.HandleFunc("GET /trash", s.handleTrash)
	mux.HandleFunc("POST /trash/restore", s.handleTrashRestore)
	mux.HandleFunc("POST /trash/empty", s.handleTrashEmpty)
	mux.HandleFunc("GET /graph", s.handleGraph)
	mux.HandleFunc("GET /graph/data", s.handleGraphData)
	mux.HandleFunc("GET /graph/entities", s.handleGraphEntitiesList)
	mux.HandleFunc("GET /graph/entity", s.handleGraphEntityPanel)
	mux.HandleFunc("GET /graph/node", s.handleGraphNode)
	mux.HandleFunc("POST /graph/entities", s.handleGraphEntityUpsert)
	mux.HandleFunc("DELETE /graph/entities", s.handleGraphEntityDelete)
	mux.HandleFunc("POST /graph/relations", s.handleGraphRelationUpsert)
	mux.HandleFunc("DELETE /graph/relations", s.handleGraphRelationDelete)

	if s.deps.MCP != nil {
		mux.Handle("/mcp", s.deps.MCP.HTTPHandler())
	}
	mux.HandleFunc("GET /mcp/info", s.handleMCPInfo)

	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	return s.sameOrigin(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("ok"))
}

func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) && !sameOriginRequest(r) {
			http.Error(w, "cross-origin request blocked", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func sameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}
	if !isLoopbackHost(r.Host) {
		return false
	}
	return sameHost(origin, r.Host)
}

func sameHost(raw, host string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Host == host
}

func isLoopbackHost(hostPort string) bool {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type Alert struct {
	Kind    string
	Message string
}

type page struct {
	Title  string
	Alerts []Alert
	Data   any
}

func (s *Server) render(w http.ResponseWriter, name string, status int, pd page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if s.tmpl == nil {
		fmt.Fprintf(w, `<html><body><div class="alert alert-error">template error: %v</div></body></html>`, s.tmplErr)
		return
	}
	if err := s.tmpl.ExecuteTemplate(w, name, pd); err != nil {
		fmt.Fprintf(w, `<html><body><div class="alert alert-error">render error: %v</div></body></html>`, err)
	}
}

func (s *Server) refreshBM25(ctx context.Context) {
	if s.deps.BM25 == nil || s.deps.Versioner == nil || s.deps.Vector == nil {
		return
	}
	_ = s.deps.BM25.Refresh(ctx, s.deps.Versioner, s.deps.Vector)
}
