package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/state"
)

type ConnectorFactory func(typ string) (connector.Connector, error)

type Options struct {
	Sources    config.SourcesConfig
	Connector  ConnectorFactory
	Sink       connector.Sink
	State      *state.Store
	Tombstones *state.TombstoneStore
	Env        connector.EnvLookup
	Only       string
	Now        func() time.Time
	// AfterBatch runs once after every source finished, as the batch-end
	// hook for deferred work (e.g. lazy community refresh). Fail-open:
	// errors are logged by the caller, not propagated into source results.
	AfterBatch func(ctx context.Context) error
}

type SourceResult struct {
	Name  string
	Type  string
	Items int
	Err   error
}

func Run(ctx context.Context, opt Options) []SourceResult {
	now := opt.Now
	if now == nil {
		now = time.Now
	}

	var results []SourceResult
	for _, src := range opt.Sources.Sources {
		if opt.Only != "" && src.Name != opt.Only {
			continue
		}
		results = append(results, runOne(ctx, opt, src, now))
	}
	if opt.AfterBatch != nil {
		_ = opt.AfterBatch(ctx)
	}
	return results
}

func runOne(ctx context.Context, opt Options, src config.SourceInstance, now func() time.Time) SourceResult {
	res := SourceResult{Name: src.Name, Type: src.Type}
	key := src.Type + ":" + src.Name

	fail := func(err error) SourceResult {
		res.Err = err
		if opt.State != nil {
			_ = opt.State.RecordError(key, now(), err.Error())
		}
		return res
	}

	conn, err := opt.Connector(src.Type)
	if err != nil {
		return fail(fmt.Errorf("resolve connector type %q: %w", src.Type, err))
	}

	cfg := connector.Config{Name: src.Name, Type: src.Type, Config: src.Config, Secrets: src.Secrets}
	if err := conn.Resolve(ctx, cfg, opt.Env); err != nil {
		return fail(fmt.Errorf("resolve: %w", err))
	}

	var cursor connector.Cursor
	if opt.State != nil {
		cursor = connector.Cursor{Value: opt.State.Cursor(key)}
	}

	out := make(chan connector.Document)
	errCh := make(chan error, 1)
	var newCursor connector.Cursor
	var info connector.FetchInfo
	go func() {
		var ferr error
		newCursor, info, ferr = conn.Fetch(ctx, cursor, out)
		errCh <- ferr
	}()

	seen := make(map[string]struct{})
	var writeErr error
	for d := range out {
		if opt.Tombstones != nil && opt.Tombstones.Contains(src.Name, d.ID) {
			continue
		}
		seen[d.ID] = struct{}{}
		if err := opt.Sink.Write(ctx, d); err != nil && writeErr == nil {
			writeErr = fmt.Errorf("write %s: %w", d.ID, err)
		}
		res.Items++
	}
	fetchErr := <-errCh

	if fetchErr != nil {
		return fail(fmt.Errorf("fetch: %w", fetchErr))
	}
	if writeErr != nil {
		return fail(writeErr)
	}

	if info.FullReconcile || len(info.PrunePrefixes) > 0 {
		if err := opt.Sink.Prune(ctx, src.Name, seen, info.PrunePrefixes...); err != nil {
			return fail(fmt.Errorf("prune: %w", err))
		}
	}

	if opt.State != nil {
		if err := opt.State.Advance(key, newCursor.Value, now()); err != nil {
			return fail(fmt.Errorf("advance state: %w", err))
		}
	}

	return res
}
