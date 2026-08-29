package ingest

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alterfo/kb/internal/config"
	"github.com/alterfo/kb/internal/connector"
	"github.com/alterfo/kb/internal/state"
)

type fakeConnector struct {
	typ           string
	docs          []connector.Document
	nextCursor    string
	fetchErr      error
	fullRecon     bool
	prunePrefixes []string
	resolveErr    error
	resolveArgs   *connector.Config
}

func (f *fakeConnector) Type() string { return f.typ }

func (f *fakeConnector) Resolve(ctx context.Context, cfg connector.Config, env connector.EnvLookup) error {
	f.resolveArgs = &cfg
	return f.resolveErr
}

func (f *fakeConnector) Fetch(ctx context.Context, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error) {
	defer close(out)
	if f.fetchErr != nil {
		return since, connector.FetchInfo{}, f.fetchErr
	}
	for _, d := range f.docs {
		out <- d
	}
	return connector.Cursor{Value: f.nextCursor}, connector.FetchInfo{ItemCount: len(f.docs), FullReconcile: f.fullRecon, PrunePrefixes: f.prunePrefixes}, nil
}

type fakeSink struct {
	writes    []connector.Document
	writeErr  error
	pruneArgs []map[string]struct{}
	prunePfx  [][]string
	pruneErr  error
}

func (f *fakeSink) Write(ctx context.Context, d connector.Document) error {
	f.writes = append(f.writes, d)
	return f.writeErr
}

func (f *fakeSink) Prune(ctx context.Context, sourceName string, seen map[string]struct{}, prefixes ...string) error {
	f.pruneArgs = append(f.pruneArgs, seen)
	f.prunePfx = append(f.prunePfx, prefixes)
	return f.pruneErr
}

func (f *fakeSink) Tombstone(ctx context.Context, sourceName, id string) error { return nil }

func factoryFor(conns map[string]connector.Connector) ConnectorFactory {
	return func(typ string) (connector.Connector, error) {
		c, ok := conns[typ]
		if !ok {
			return nil, errors.New("unknown type " + typ)
		}
		return c, nil
	}
}

func fixedNow() func() time.Time {
	t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func TestRunAdvancesCursorOnSuccess(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	conn := &fakeConnector{typ: "github", docs: []connector.Document{{ID: "1", Source: "gh"}}, nextCursor: "cursor-1"}
	sk := &fakeSink{}

	results := Run(context.Background(), Options{
		Sources:   config.SourcesConfig{Sources: []config.SourceInstance{{Name: "gh", Type: "github"}}},
		Connector: factoryFor(map[string]connector.Connector{"github": conn}),
		Sink:      sk,
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
	})

	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Items != 1 {
		t.Fatalf("Items = %d, want 1", results[0].Items)
	}
	if cur := st.Cursor("github:gh"); cur != "cursor-1" {
		t.Fatalf("Cursor() = %q, want cursor-1", cur)
	}
	if len(sk.writes) != 1 || sk.writes[0].ID != "1" {
		t.Fatalf("writes = %+v", sk.writes)
	}
}

func TestRunSkipsTombstonedDocumentsAndPrunesLingeringFiles(t *testing.T) {
	dir := t.TempDir()
	st, err := state.OpenStore(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	ts, err := state.OpenTombstoneStore(filepath.Join(dir, "tombstones.json"))
	if err != nil {
		t.Fatalf("OpenTombstoneStore: %v", err)
	}
	if err := ts.Add("gh", "retired"); err != nil {
		t.Fatalf("ts.Add: %v", err)
	}

	conn := &fakeConnector{typ: "github", docs: []connector.Document{
		{ID: "retired", Source: "gh"},
		{ID: "live", Source: "gh"},
	}}
	sk := &fakeSink{}

	results := Run(context.Background(), Options{
		Sources:    config.SourcesConfig{Sources: []config.SourceInstance{{Name: "gh", Type: "github"}}},
		Connector:  factoryFor(map[string]connector.Connector{"github": conn}),
		Sink:       sk,
		State:      st,
		Tombstones: ts,
		Env:        func(string) (string, bool) { return "", false },
		Now:        fixedNow(),
	})

	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	if len(sk.writes) != 1 || sk.writes[0].ID != "live" {
		t.Fatalf("writes = %+v, want only the non-tombstoned doc", sk.writes)
	}
	if len(sk.pruneArgs) != 0 {
		t.Fatalf("prune called without FullReconcile")
	}

	conn.fullRecon = true
	results = Run(context.Background(), Options{
		Sources:    config.SourcesConfig{Sources: []config.SourceInstance{{Name: "gh", Type: "github"}}},
		Connector:  factoryFor(map[string]connector.Connector{"github": conn}),
		Sink:       sk,
		State:      st,
		Tombstones: ts,
		Env:        func(string) (string, bool) { return "", false },
		Now:        fixedNow(),
	})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("full-reconcile results = %+v", results)
	}
	if len(sk.pruneArgs) != 1 {
		t.Fatalf("prune calls = %d, want 1", len(sk.pruneArgs))
	}
	if _, ok := sk.pruneArgs[0]["retired"]; ok {
		t.Fatalf("prune seen = %v, want tombstoned id excluded so prune deletes the lingering file", sk.pruneArgs[0])
	}
}

func TestRunOnlyFiltersToNamedSource(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	connA := &fakeConnector{typ: "github"}
	connB := &fakeConnector{typ: "gitlab"}

	results := Run(context.Background(), Options{
		Sources: config.SourcesConfig{Sources: []config.SourceInstance{
			{Name: "a", Type: "github"},
			{Name: "b", Type: "gitlab"},
		}},
		Connector: factoryFor(map[string]connector.Connector{"github": connA, "gitlab": connB}),
		Sink:      &fakeSink{},
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Only:      "b",
		Now:       fixedNow(),
	})

	if len(results) != 1 || results[0].Name != "b" {
		t.Fatalf("results = %+v, want only source b", results)
	}
}

func TestRunRollsBackCursorOnFetchError(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := st.Advance("github:gh", "old-cursor", now); err != nil {
		t.Fatalf("seed Advance: %v", err)
	}

	conn := &fakeConnector{typ: "github", fetchErr: errors.New("boom")}
	results := Run(context.Background(), Options{
		Sources:   config.SourcesConfig{Sources: []config.SourceInstance{{Name: "gh", Type: "github"}}},
		Connector: factoryFor(map[string]connector.Connector{"github": conn}),
		Sink:      &fakeSink{},
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
	})

	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("results = %+v, want an error", results)
	}
	if cur := st.Cursor("github:gh"); cur != "old-cursor" {
		t.Fatalf("Cursor() after failure = %q, want old-cursor (rollback)", cur)
	}
	sst, ok := st.Get("github:gh")
	if !ok || sst.LastError == "" {
		t.Fatalf("expected LastError recorded, got %+v", sst)
	}
}

func TestRunRollsBackOnWriteError(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := st.Advance("github:gh", "old-cursor", time.Now()); err != nil {
		t.Fatalf("seed Advance: %v", err)
	}

	conn := &fakeConnector{typ: "github", docs: []connector.Document{{ID: "1", Source: "gh"}}, nextCursor: "new-cursor"}
	sk := &fakeSink{writeErr: errors.New("disk full")}

	results := Run(context.Background(), Options{
		Sources:   config.SourcesConfig{Sources: []config.SourceInstance{{Name: "gh", Type: "github"}}},
		Connector: factoryFor(map[string]connector.Connector{"github": conn}),
		Sink:      sk,
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
	})

	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("results = %+v, want an error", results)
	}
	if cur := st.Cursor("github:gh"); cur != "old-cursor" {
		t.Fatalf("Cursor() after write error = %q, want old-cursor (rollback)", cur)
	}
}

func TestRunPrunesOnFullReconcile(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	conn := &fakeConnector{typ: "wiki", docs: []connector.Document{{ID: "1", Source: "w"}, {ID: "2", Source: "w"}}, fullRecon: true}
	sk := &fakeSink{}

	results := Run(context.Background(), Options{
		Sources:   config.SourcesConfig{Sources: []config.SourceInstance{{Name: "w", Type: "wiki"}}},
		Connector: factoryFor(map[string]connector.Connector{"wiki": conn}),
		Sink:      sk,
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
	})

	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	if len(sk.pruneArgs) != 1 {
		t.Fatalf("expected exactly 1 Prune call, got %d", len(sk.pruneArgs))
	}
	seen := sk.pruneArgs[0]
	if _, ok := seen["1"]; !ok {
		t.Fatalf("expected seen to contain id 1, got %v", seen)
	}
	if _, ok := seen["2"]; !ok {
		t.Fatalf("expected seen to contain id 2, got %v", seen)
	}
}

func TestRunSkipsPruneWithoutFullReconcile(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	conn := &fakeConnector{typ: "wiki", docs: []connector.Document{{ID: "1", Source: "w"}}, fullRecon: false}
	sk := &fakeSink{}

	Run(context.Background(), Options{
		Sources:   config.SourcesConfig{Sources: []config.SourceInstance{{Name: "w", Type: "wiki"}}},
		Connector: factoryFor(map[string]connector.Connector{"wiki": conn}),
		Sink:      sk,
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
	})

	if len(sk.pruneArgs) != 0 {
		t.Fatalf("expected no Prune calls, got %d", len(sk.pruneArgs))
	}
}

func TestRunPrunesScopedPrefixesOnIncrementalRun(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	conn := &fakeConnector{
		typ:           "github",
		docs:          []connector.Document{{ID: "acme/widgets:contents:README.md", Source: "gh"}},
		fullRecon:     false,
		prunePrefixes: []string{"acme/widgets:contents:", "acme/widgets:wiki:"},
	}
	sk := &fakeSink{}

	results := Run(context.Background(), Options{
		Sources:   config.SourcesConfig{Sources: []config.SourceInstance{{Name: "gh", Type: "github"}}},
		Connector: factoryFor(map[string]connector.Connector{"github": conn}),
		Sink:      sk,
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
	})

	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	if len(sk.pruneArgs) != 1 {
		t.Fatalf("expected exactly 1 Prune call on incremental run with prefixes, got %d", len(sk.pruneArgs))
	}
	if len(sk.prunePfx) != 1 || len(sk.prunePfx[0]) != 2 || sk.prunePfx[0][0] != "acme/widgets:contents:" || sk.prunePfx[0][1] != "acme/widgets:wiki:" {
		t.Fatalf("prune prefixes = %v, want [acme/widgets:contents: acme/widgets:wiki:]", sk.prunePfx)
	}
}

func TestRunContinuesToNextSourceAfterFailure(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	failing := &fakeConnector{typ: "github", fetchErr: errors.New("boom")}
	ok := &fakeConnector{typ: "gitlab", docs: []connector.Document{{ID: "1", Source: "b"}}}

	results := Run(context.Background(), Options{
		Sources: config.SourcesConfig{Sources: []config.SourceInstance{
			{Name: "a", Type: "github"},
			{Name: "b", Type: "gitlab"},
		}},
		Connector: factoryFor(map[string]connector.Connector{"github": failing, "gitlab": ok}),
		Sink:      &fakeSink{},
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
	})

	if len(results) != 2 {
		t.Fatalf("results = %+v, want 2 entries", results)
	}
	if results[0].Err == nil {
		t.Fatal("expected first source to have an error")
	}
	if results[1].Err != nil {
		t.Fatalf("expected second source to succeed, got %v", results[1].Err)
	}
}

func TestRunUnknownConnectorTypeRecordsErrorAndContinues(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	ok := &fakeConnector{typ: "gitlab"}

	results := Run(context.Background(), Options{
		Sources: config.SourcesConfig{Sources: []config.SourceInstance{
			{Name: "a", Type: "unknown-type"},
			{Name: "b", Type: "gitlab"},
		}},
		Connector: factoryFor(map[string]connector.Connector{"gitlab": ok}),
		Sink:      &fakeSink{},
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
	})

	if len(results) != 2 {
		t.Fatalf("results = %+v, want 2 entries", results)
	}
	if results[0].Err == nil {
		t.Fatal("expected unknown-type source to have an error")
	}
	if results[1].Err != nil {
		t.Fatalf("expected second source to succeed, got %v", results[1].Err)
	}
}

func TestRunPassesConfigAndSecretsToResolve(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	conn := &fakeConnector{typ: "github"}

	Run(context.Background(), Options{
		Sources: config.SourcesConfig{Sources: []config.SourceInstance{
			{Name: "gh", Type: "github", Config: map[string]string{"org": "acme"}, Secrets: map[string]string{"token": "GH_TOKEN"}},
		}},
		Connector: factoryFor(map[string]connector.Connector{"github": conn}),
		Sink:      &fakeSink{},
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
	})

	if conn.resolveArgs == nil {
		t.Fatal("expected Resolve to be called")
	}
	if conn.resolveArgs.Config["org"] != "acme" {
		t.Fatalf("Config = %v", conn.resolveArgs.Config)
	}
	if conn.resolveArgs.Secrets["token"] != "GH_TOKEN" {
		t.Fatalf("Secrets = %v", conn.resolveArgs.Secrets)
	}
}

func TestRunCallsAfterBatchAtEndOfBatch(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	conn := &fakeConnector{typ: "github", docs: []connector.Document{{ID: "1", Source: "gh"}}, nextCursor: "cursor-1"}
	sk := &fakeSink{}

	var afterCalls int
	results := Run(context.Background(), Options{
		Sources:   config.SourcesConfig{Sources: []config.SourceInstance{{Name: "gh", Type: "github"}}},
		Connector: factoryFor(map[string]connector.Connector{"github": conn}),
		Sink:      sk,
		State:     st,
		Env:       func(string) (string, bool) { return "", false },
		Now:       fixedNow(),
		AfterBatch: func(ctx context.Context) error {
			afterCalls++
			return errors.New("refresh exploded")
		},
	})

	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("AfterBatch error must not fail the batch: results = %+v", results)
	}
	if afterCalls != 1 {
		t.Fatalf("AfterBatch calls = %d, want 1", afterCalls)
	}
}

func TestRunAfterBatchNilIsNoop(t *testing.T) {
	st, err := state.OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	conn := &fakeConnector{typ: "github", docs: []connector.Document{{ID: "1", Source: "gh"}}, nextCursor: "cursor-1"}
	sk := &fakeSink{}

	results := Run(context.Background(), Options{
		Sources:    config.SourcesConfig{Sources: []config.SourceInstance{{Name: "gh", Type: "github"}}},
		Connector:  factoryFor(map[string]connector.Connector{"github": conn}),
		Sink:       sk,
		State:      st,
		Env:        func(string) (string, bool) { return "", false },
		Now:        fixedNow(),
		AfterBatch: nil,
	})

	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
}
