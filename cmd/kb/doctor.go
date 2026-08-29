package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/llm"
	"github.com/alterfo/kb/internal/state"
	"github.com/alterfo/kb/internal/store/sqlite"
)

func runDoctorCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fset.SetOutput(stderr)
	if err := fset.Parse(args); err != nil {
		return 2
	}

	ctx := context.Background()
	failed := false

	failed = reportLLMHealth(ctx, env, stdout) || failed
	failed = reportIndex(ctx, env, stdout) || failed
	failed = reportSources(env, stdout) || failed

	if failed {
		fmt.Fprintln(stdout, "doctor: problems found")
		return 1
	}
	fmt.Fprintln(stdout, "doctor: all checks passed")
	return 0
}

func reportLLMHealth(ctx context.Context, env config.Env, stdout io.Writer) bool {
	chat := llm.NewClient(llm.Config{
		BaseURL:           env.LLMBaseURL,
		NoProxyHosts:      env.NoProxy,
		DefaultEmbedModel: env.EmbedModel,
		RequestTimeout:    env.LLMTimeout,
	})
	queryEmbedURL := env.EmbedBaseURL
	if queryEmbedURL == "" {
		queryEmbedURL = config.DefaultLocalLLMURL
	}
	embed := llm.NewClient(llm.Config{
		BaseURL:           queryEmbedURL,
		NoProxyHosts:      env.NoProxy,
		DefaultEmbedModel: env.EmbedModel,
		RequestTimeout:    env.LLMTimeout,
	})

	fmt.Fprintf(stdout, "llm chat (host):  %s (model=%s)\n", env.LLMBaseURL, env.LLMModel)
	fmt.Fprintf(stdout, "query embed (local): %s (model=%s)\n", queryEmbedURL, env.EmbedModel)
	fmt.Fprintf(stdout, "index embed (bulk):   %s (model=%s)\n", env.EmbedIndexBaseURL, env.EmbedModel)
	failed := false

	dim, err := embed.Dim(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "  query embed dim: FAILED (%v)\n", err)
		failed = true
	} else {
		fmt.Fprintf(stdout, "  query embed dim: ok (%d)\n", dim)
	}

	if _, err := chat.Chat(ctx, llm.ChatRequest{
		Model:    env.LLMModel,
		Messages: []llm.ChatMessage{{Role: "user", Content: "ping"}},
	}); err != nil {
		fmt.Fprintf(stdout, "  chat: FAILED (%v)\n", err)
		failed = true
	} else {
		fmt.Fprintf(stdout, "  chat: ok\n")
	}

	indexEmbedURL := env.EmbedIndexBaseURL
	if indexEmbedURL == "" {
		indexEmbedURL = env.LLMBaseURL
	}
	indexEmbed := llm.NewClient(llm.Config{
		BaseURL:           indexEmbedURL,
		NoProxyHosts:      env.NoProxy,
		DefaultEmbedModel: env.EmbedModel,
		RequestTimeout:    env.LLMTimeout,
	})
	if dim, err := indexEmbed.Dim(ctx); err != nil {
		fmt.Fprintf(stdout, "  index embed dim: FAILED (%v)\n", err)
		failed = true
	} else {
		fmt.Fprintf(stdout, "  index embed dim: ok (%d)\n", dim)
	}
	return failed
}

func reportIndex(ctx context.Context, env config.Env, stdout io.Writer) bool {
	dbPath := filepath.Join(env.PersistDir, "kb.db")
	fmt.Fprintf(stdout, "index: %s\n", dbPath)

	if _, err := os.Stat(dbPath); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(stdout, "  status: no index yet (run kb sync or kb reindex)")
		return false
	}

	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(stdout, "  status: FAILED to open db (%v)\n", err)
		return true
	}
	defer db.Close()

	failed := false
	ver, err := db.CorpusVersion(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "  corpus_version: FAILED (%v)\n", err)
		failed = true
	}
	dim, hasDim, err := db.EmbedDim(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "  embed_dim: FAILED (%v)\n", err)
		failed = true
	}
	chunks, err := db.ChunkCount(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "  chunks: FAILED (%v)\n", err)
		failed = true
	}

	gs := sqlite.NewGraphStore(db)
	entities, err := gs.AllEntities(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "  entities: FAILED (%v)\n", err)
		failed = true
	}
	relations, err := gs.AllRelations(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "  relations: FAILED (%v)\n", err)
		failed = true
	}
	communities, err := gs.AllCommunities(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "  communities: FAILED (%v)\n", err)
		failed = true
	}
	if !failed {
		dimSuffix := ""
		if !hasDim {
			dimSuffix = " (unset)"
		}
		fmt.Fprintf(stdout, "  status: ok (corpus_version=%d, embed_dim=%d%s, chunks=%d, entities=%d, relations=%d, communities=%d)\n",
			ver, dim, dimSuffix, chunks, len(entities), len(relations), len(communities))
	}
	return failed
}

func reportSources(env config.Env, stdout io.Writer) bool {
	sourcesPath := filepath.Join(env.KBRoot, "sources.yaml")
	cfg, err := config.LoadSourcesFile(sourcesPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stdout, "sources: none (no sources.yaml at %s)\n", sourcesPath)
			return false
		}
		fmt.Fprintf(stdout, "sources: FAILED to load %s (%v)\n", sourcesPath, err)
		return true
	}

	st, err := state.OpenStore(filepath.Join(env.PersistDir, ".sync-state.json"))
	if err != nil {
		fmt.Fprintf(stdout, "sources: FAILED to open sync state (%v)\n", err)
		return true
	}

	rows := append([]config.SourceInstance(nil), cfg.Sources...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Type != rows[j].Type {
			return rows[i].Type < rows[j].Type
		}
		return rows[i].Name < rows[j].Name
	})

	fmt.Fprintf(stdout, "sources: %d configured (stale threshold %s)\n", len(rows), env.StaleAfter)
	now := time.Now()
	for _, src := range rows {
		key := src.Type + ":" + src.Name
		fmt.Fprintf(stdout, "  - %s (%s)", src.Name, src.Type)

		for _, field := range sortedSecretFields(src.Secrets) {
			_, present := os.LookupEnv(src.Secrets[field])
			mark := "unset"
			if present {
				mark = "set"
			}
			fmt.Fprintf(stdout, " %s=%s", field, mark)
		}

		if s, ok := st.Get(key); ok && !s.LastSyncAt.IsZero() {
			stale := now.Sub(s.LastSyncAt) > env.StaleAfter
			flag := "fresh"
			if stale {
				flag = "stale"
			}
			fmt.Fprintf(stdout, " last_sync=%s (%s)", s.LastSyncAt.Format(time.RFC3339), flag)
			if s.LastError != "" {
				fmt.Fprintf(stdout, " last_error=%q", s.LastError)
			}
		} else {
			fmt.Fprintf(stdout, " never synced (stale)")
		}
		fmt.Fprintln(stdout)
	}
	return false
}

func sortedSecretFields(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
