package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/connector/registry"
	"github.com/alterfo/kb/internal/ingest"
	"github.com/alterfo/kb/internal/sink"
	"github.com/alterfo/kb/internal/state"
	"github.com/alterfo/kb/internal/transport"
)

func runSyncCmd(args []string, env config.Env, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("sync", flag.ContinueOnError)
	fset.SetOutput(stderr)
	all := fset.Bool("all", false, "sync all configured sources")
	source := fset.String("source", "", "sync only the named source")
	api := fset.String("api", "", "push documents to a running server API at this base URL instead of writing files")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if !*all && *source == "" {
		fmt.Fprintln(stderr, "sync: either --all or --source=NAME is required")
		return 2
	}

	sourcesPath := filepath.Join(env.KBRoot, "sources.yaml")
	sourcesCfg, err := config.LoadSourcesFile(sourcesPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stdout, "sync: no sources.yaml at %s, nothing to do\n", sourcesPath)
			return 0
		}
		fmt.Fprintf(stderr, "sync: %v\n", err)
		return 1
	}

	statePath := filepath.Join(env.PersistDir, ".sync-state.json")
	st, err := state.OpenStore(statePath)
	if err != nil {
		fmt.Fprintf(stderr, "sync: opening state: %v\n", err)
		return 1
	}
	ts, err := state.OpenTombstoneStore(filepath.Join(env.PersistDir, ".tombstones.json"))
	if err != nil {
		fmt.Fprintf(stderr, "sync: opening tombstones: %v\n", err)
		return 1
	}

	var sinkImpl connector.Sink = sink.NewFileSink(env.KBRoot)
	if *api != "" {
		sinkImpl = sink.NewAPISink(transport.NewClient(transport.Config{}), *api)
	}

	var refreshOnce sync.Once
	var refreshBundle *engineBundle
	var refreshErr error
	defer func() {
		if refreshBundle != nil {
			refreshBundle.close()
		}
	}()

	results := ingest.Run(context.Background(), ingest.Options{
		Sources:    sourcesCfg,
		Connector:  registry.New,
		Sink:       sinkImpl,
		State:      st,
		Tombstones: ts,
		Env:        func(key string) (string, bool) { return os.LookupEnv(key) },
		Only:       *source,
		Now:        time.Now,
		AfterBatch: func(c context.Context) error {
			refreshOnce.Do(func() {
				refreshBundle, refreshErr = newEngineBundle(env)
			})
			if refreshErr != nil {
				log.Printf("sync: open engine bundle for community refresh: %v", refreshErr)
				return nil
			}
			if _, err := refreshBundle.updater.RefreshStaleCommunities(c); err != nil {
				log.Printf("sync: refresh stale communities: %v", err)
			}
			return nil
		},
	})

	if len(results) == 0 {
		fmt.Fprintln(stdout, "sync: no matching sources")
		return 0
	}

	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(stderr, "sync: %s (%s): %v\n", r.Name, r.Type, r.Err)
			continue
		}
		fmt.Fprintf(stdout, "sync: %s (%s): %d item(s)\n", r.Name, r.Type, r.Items)
	}
	if failed > 0 {
		return 1
	}
	return 0
}
