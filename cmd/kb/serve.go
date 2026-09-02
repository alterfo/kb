package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/governance"
	"github.com/alterfo/kb/internal/mcp"
	"github.com/alterfo/kb/internal/web"
)

func runServeCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("serve", flag.ContinueOnError)
	fset.SetOutput(stderr)
	addr := fset.String("addr", "127.0.0.1:8080", "listen address")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if !isLoopbackAddr(*addr) {
		fmt.Fprintln(stderr, "serve: refusing non-loopback listen address: the dashboard has no authentication and exposes destructive routes. Bind to 127.0.0.1 or [::1], or expose it via an SSH tunnel / reverse proxy instead")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bundle, err := newEngineBundle(env)
	if err != nil {
		fmt.Fprintf(stderr, "serve: opening db: %v\n", err)
		return 1
	}
	defer bundle.close()

	mcpSrv := mcp.NewServer(mcp.Deps{
		Root:                 env.KBRoot,
		Vector:               bundle.vector,
		Versioner:            bundle.db,
		BM25:                 bundle.bm25,
		Graph:                bundle.graph,
		Indexer:              bundle.indexer,
		Chat:                 bundle.chat,
		Embed:                bundle.embed,
		Reranker:             rerankFromEnv(env, bundle.chat),
		LLMModel:             env.LLMModel,
		EmbedModel:           env.EmbedModel,
		Hybrid:               env.Hybrid,
		AuthorityBonus:       env.AuthorityBonus,
		RRFK:                 env.RRFK,
		DefaultK:             env.TopK,
		CandidateK:           env.CandidateK,
		PerDocCap:            env.PerDocCap,
		SetMaxRounds:         env.SetMaxRounds,
		IntraDocBudget:       env.IntraDocBudget,
		AbstainThreshold:     env.AbstainThreshold,
		SupersedeMode:        env.SupersedeMode,
		DetectContradictions: env.DetectContradictions,
		QualifierFilter:      env.QualifierFilter,
		ANNPrefilter:         env.ANNPrefilter,
		RollingMemory:        env.AskRollingWindow,
		SourcesPath:          filepath.Join(env.KBRoot, "sources.yaml"),
	})

	webSrv := web.NewServer(web.Deps{
		Root:                 env.KBRoot,
		PersistDir:           env.PersistDir,
		BaseCtx:              ctx,
		Vector:               bundle.vector,
		Versioner:            bundle.db,
		BM25:                 bundle.bm25,
		Graph:                bundle.graph,
		GraphUpdater:         bundle.updater,
		Indexer:              bundle.indexer,
		History:              bundle.history,
		MCP:                  mcpSrv,
		Chat:                 bundle.chat,
		Embed:                bundle.embed,
		Reranker:             rerankFromEnv(env, bundle.chat),
		LLMModel:             env.LLMModel,
		EmbedModel:           env.EmbedModel,
		Hybrid:               env.Hybrid,
		AuthorityBonus:       env.AuthorityBonus,
		RRFK:                 env.RRFK,
		DefaultK:             env.TopK,
		CandidateK:           env.CandidateK,
		PerDocCap:            env.PerDocCap,
		SetMaxRounds:         env.SetMaxRounds,
		IntraDocBudget:       env.IntraDocBudget,
		AbstainThreshold:     env.AbstainThreshold,
		SupersedeMode:        env.SupersedeMode,
		DetectContradictions: env.DetectContradictions,
		QualifierFilter:      env.QualifierFilter,
		ANNPrefilter:         env.ANNPrefilter,
		RollingMemory:        env.AskRollingWindow,
		SourcesPath:          filepath.Join(env.KBRoot, "sources.yaml"),
		StatePath:            filepath.Join(env.PersistDir, ".sync-state.json"),
		StaleAfter:           env.StaleAfter,
		EnvLookup:            os.LookupEnv,
		Governance:           governance.New(env.KBRoot, bundle.indexer, bundle.chat, env.LLMModel),
	})

	httpSrv := &http.Server{Addr: *addr, Handler: webSrv.Handler()}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	fmt.Fprintf(stdout, "serve: listening on %s (root=%s persist=%s)\n", *addr, env.KBRoot, env.PersistDir)
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(stderr, "serve: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		<-errCh
		return 0
	}
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
