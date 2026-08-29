# Adding a new connector

A connector produces `connector.Document`s from an external service and hands
them to the common ingest path. This guide is the checklist a new connector
must satisfy to land (mirrors the test axes used in Tasks 12–18).

## Steps

1. Create package `internal/connectors/<name>` implementing `connector.Connector`:
   - `Type() string` — the registry key (e.g. `"my-service"`);
   - `Resolve(ctx, cfg connector.Config, env connector.EnvLookup) error` —
     validate `cfg.Config`, resolve secret values from `env` (never store them);
   - `Fetch(ctx, since connector.Cursor, out chan<- connector.Document) (connector.Cursor, connector.FetchInfo, error)` —
     emit documents, return the new cursor and fetch info (item counts, errors).
2. Register the factory in `internal/connector/registry` via `init()`:
   `registry.Register("my-service", func() connector.Connector { return New() })`.
3. Wire the blank import in `cmd/kb/connectors.go`.
4. Secrets: declare env-var *names* in `AuthSpec`/`sources.yaml`; read values
   via `EnvLookup` at resolve time. Never persist or log values — the
   presence-only invariant is tested.
5. Reuse `internal/transport` for pagination/retry/ratelimit/ETag — do not
   write your own HTTP layer. For services that need a SOCKS5 proxy, use
   `transport.SOCKS5DialContext` + `NewProxyBypassTransportWithDialContext`
   driven by `KB_SOCKS_PROXY` (see the Discord connector).
6. Incrementality: use cursors through `internal/state` (advance-on-success +
   rollback); `PruneOrphans`/tombstones only on full reconcile.
7. Frontmatter: service-specific keys on top of the reserved common ones
   (`source, id, kind, title, url, updated_at, visibility, summary` — `kind`,
   `visibility`, and `summary` are typed fields, so do not put them in
   `Frontmatter`);
   add a golden render test in
   `internal/render/testdata`.
8. Document the source in `docs/sources.md` (config + secrets fields) and add
   it to the connector table in `README.md`.

## Required test axes

- auth (header/query/token resolve, fail-open when the secret env var is unset)
- pagination (offset/page/cursor; explicit stop condition, `go test -timeout`)
- incremental cursor (advance-on-success, rollback on failure)
- frontmatter golden (`testdata/` render output)
- tombstone (deleted remote item is not re-imported)
- prune only on `FullReconcile`
- ratelimit/backoff with a fake clock
- fail-open: any API/parse error returns a partial result + error, no panic

## Contract recap

```go
type Connector interface {
    Type() string
    Resolve(ctx context.Context, cfg Config, env EnvLookup) error
    Fetch(ctx context.Context, since Cursor, out chan<- Document) (Cursor, FetchInfo, error)
}
```

- `Document` — source, id, url, updated_at, title, body, visibility (optional),
  summary (optional), extra frontmatter fields (map).
- `Sink` — `Write`, `Prune` (full reconcile only), `Tombstone`.
- `state.Store` — cursor persistence and sync metadata (`last_sync_at`,
  `last_error`) per `<type>:<name>`.
