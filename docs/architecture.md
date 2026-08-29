# Architecture: GraphRAG pipeline

`kb` turns heterogeneous sources (code hosts, wikis, chats, task trackers,
MCP servers, files) into a single searchable corpus: markdown documents with
YAML frontmatter, vector embeddings, an in-memory BM25 index, and a knowledge
graph (entities / relations / communities) — all persisted in one SQLite file
except the BM25 index, which is rebuilt from `chunks` on corpus invalidation.

## Data flow

```
                 ┌──────────────────────────────────────────────────────┐
                 │                      Ingest                            │
                 │  Connector.Fetch → Document (chan)                     │
                 │  internal/connector + internal/connectors/*            │
                 │  - pagination/retry/ratelimit/ETag/SOCKS5: internal/transport │
                 │  - cursor: advance-on-success + rollback (state)       │
                 └───────────────────────────┬────────────────────────────┘
                                             │
                                             ▼
        Document ──► render ──► markdown + YAML frontmatter
                 (source, id, url, updated_at, title, + service-specific keys)
                                             │
                                             ▼
        sink: FileSink (default) | APISink | TeeSink   ──►  $KB_ROOT/**
        state: .sync-state.json (cursor, last_sync_at, last_error), tombstones.json
                                             │
                                             ▼
        engine (indexer): AddOrUpdateDocument / RemoveDocument / Reindex
                 │                                        │
                 ▼                                        ▼
      chunking (sentences + ChatChunker)      graph: LLM extraction
      → VectorStore.Upsert (BLOBs, cosine)    entities/relations →
      → BM25 rebuild (corpus_version)         merge/dedup → communities
                                              (Louvain default, hierarchical
                                              Leiden via KB_COMMUNITY_ALGO,
                                              Louvain fallback) → summaries
                 │                                        │
                 └──────────────┬─────────────────────────┘
                                ▼
        retriever (hybrid + graph-aware fusion)
        dense multi-query + BM25 → RRF → authority prior → per-doc cap
        → entity-linking → neighbor expansion → community context
                                │
                                ▼
        reranker (noop | llm | onnx) — optional, fail-open
                                │
                                ▼
        got.Orchestrator (Graph-of-Thoughts)
        decompose → parallel [retrieve → score_coverage → synthesize]
        → aggregate → find_gaps → 1×refine → finalize
                                │
                 ┌──────────────┴───────────────┐
                 ▼                              ▼
        MCP server (internal/mcp)      Web dashboard (internal/web)
        tools: search, ask, get_document,      routes: /, /search, /ask (SSE),
        list_sources, add_note, add_source,    /ask/history, /documents (+view/edit/delete),
        graph_query, generate_report,          /add, /integrations (+save/delete),
        reindex, status; stdio + HTTP         /reports, /graph (+entity/relation CRUD,
        (Streamable HTTP mounted on /mcp)      + /graph/data JSON), /mcp/info,
                                               /cleanup, /trash
```

## Key contracts

- Persistence is one file `$PERSIST_DIR/kb.db`: `chunks` (vectors as BLOBs,
  brute-force cosine), `entities`/`relations`/`communities` (graph),
  `kb_meta`/`corpus_version`, plus `search_history`/`ask_runs` (dashboard
  search/ask history).
- `relations` are bi-temporal: `valid_from`/`valid_to` (real-world validity,
  open-ended when NULL), `created_at`/`expired_at` (system time). On a
  conflict the old edge is closed, not overwritten (audit trail preserved);
  `RelationsAsOf(ids, t)` reconstructs the facts current at time `t`.
- `doc_id` is a stable relative path; chunk `ref_doc_id = doc_id`;
  updates soft-close old chunk versions (`valid_to`, `replaces` lineage)
  instead of deleting them; `RemoveDocument` still hard-deletes. Cross-doc
  supersession marks chunks overlapping a newer document's entities with
  `superseded_by` (soft rank penalty, never excluded from retrieval).
- Graph extraction is routed by document kind: `legal-article`/`legal-plenum`
  (deterministic structural anchors + domain prompts), `message` (chat
  two-phase extraction with per-speaker attribution), `code` or `.go` files
  (deterministic go/ast code-graph, no LLM), everything else (generic LLM
  extraction). `internal/verify` runs golden-graph diffs, citation-existence
  checks, contradiction detection, and closed-issue Q&A evaluation
  (`internal/verify/qa`) over retrieved chunks.
- `reindex`/`BuildAll` skip a document whose raw content hash
  (`doc_hashes`, keyed by `ref_doc_id`) matches the last-indexed hash, so a
  repeat reindex only re-embeds/re-extracts changed files. Chat-thread
  messages are excluded (bulk thread-gluing writes chunks later, in
  `flushChatThreads`, so hashing at read time would mark a message indexed
  before it actually was).
- Embedding dimension is probed at runtime and fixed in `kb_meta`;
  a mismatch is fail-loud (`ErrDimMismatch`); everything else is fail-open:
  each retrieval/LLM/rerank/graph step returns a partial result + error, and
  the caller degrades — never panics.
- Secrets: `sources.yaml` stores only env-var *names*; values are read via
  `EnvLookup` at resolve time; presence-only in reports/dashboards/logs.
- Proxy-bypass: hosts in `KB_NO_PROXY` (default `127.0.0.1`) connect
  directly; everything else uses `ProxyFromEnvironment`. Connectors that need
  it (Discord) can use `KB_SOCKS_PROXY` via `SOCKS5DialContext`, which
  disables the HTTP proxy for that transport.

## Design decisions (scope cuts)

- ACL/permissions: single-user local prototype; `visibility` frontmatter field
  is passthrough (reserved for a future `PermissionChecker`).
- No separate `ConflictResolver`: authority prior (`notes/approved/` weighs
  more than chat) + mandatory source citation by filename in synthesized
  answers — the user sees both facts and decides.
- Full BM25 rebuild on write is an accepted prototype tradeoff. Community
  detection is lazy: writes mark affected components `stale` and detection
  runs in batches (end of a sync batch / query-time throttle) over the
  affected components only. Community detection: Louvain by default,
  hierarchical Leiden behind `KB_COMMUNITY_ALGO=leiden` (dependency audited,
  see `docs/leiden-audit-20260821.md`); candidates for replacement at scale:
  SQLite FTS5 and incremental community updates.
