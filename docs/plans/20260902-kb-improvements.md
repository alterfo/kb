# kb Improvements Plan — 2026-08-31

## Overview

Consolidated improvement plan for kb, synthesizing three independent reviews: a primary
list from deepseek-v4-pro, critique and additions from qwen3.8, and an independent
architecture review from Claude Code. The plan covers correctness bugs in the
Graph-of-Thought (GoT) orchestrator, disaster recovery for the SQLite-backed knowledge
base, CI quality gates, retrieval metrics/observability, a move from in-memory BM25 to
FTS5, configuration hygiene, benchmark iteration speed, an ANN prefilter, SQLite
concurrency, an Ask-response cache, a feedback loop, near-duplicate detection, embedding
model migration, and guardrails against PII leakage and prompt injection.

## Context

- Adopted from `docs/plans/kb-improvements-20260831.md` (2026-08-31), itself a consensus
  of three consultations: deepseek-v4-pro (primary list), qwen3.8:latest (critique, see
  `model-duel-20260831-kb-improvements.md`), and Claude Code (independent architecture
  review, see `/tmp/claude-code-plan.md`).
- The `kb plan` subsystem (`internal/planner`) has already been removed from the project
  as mistakenly created. The Claude Code finding about RCE in its bash tool is therefore
  closed via removal, not hardening, and is not part of this plan.
- Items marked "Claude Code only" below were raised solely in the Claude Code review and
  not corroborated by the other two reviewers; treat them as lower-confidence than
  consensus items.
- Original sequencing intent: P0 items 1-3 (2 and 3 are parallel/independent) → P1 items
  4 → 5 → 6 → 7 → 8 (7 and 8 parallel) → P2 items 9 (after 6), 10 → 11, 12/13/14/15
  (independent of each other). Tasks below preserve this order; ralphex executes them
  sequentially regardless of which pairs were originally parallelizable.

## Development Approach

- Testing approach: regular (write tests alongside each change, not TDD)
- Complete each task fully before moving to the next
- Update this plan when scope changes during implementation
- Respect the original priority ordering (P0 correctness fixes first, then P1
  measurability/scale work, then P2 quality/operability work)

## Testing Strategy

- Unit tests required for every code-changing task, including property-based tests
  where the source calls for them (e.g. task 1's invariants)
- Run the full project test suite after each task before proceeding
- Prefer tests on invariants and structural properties over tests overfitted to
  specific golden strings

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Update this plan if implementation deviates from the original scope

## Technical Details

Known file references from the source review (verify current line numbers before
editing, they may have drifted since 2026-08-31):

- `decompose.go:201` — `cleanSubgoalItems` does not remap `depends_on`
- `orchestrator.go:462` — empty "Previously resolved sub-answers" for cycle-broken or
  self-dependent subgoals
- `AGENTS.md:154` and `README.md:73` — dangling links to the removed
  `docs/architecture-review.md`
- Config: 46 total `KB_*` environment variables — 33 in `internal/config/env.go` (basic
  per-variable format validation already exists there) plus 13 read directly by
  connectors/subsystems (e.g. `KB_DISCORD_TOKEN`, `KB_SOCKS_PROXY`, `KB_PLAN_*`) with no
  central validation at all. The real gap is the absence of a `kb config show` /
  effective-config dump and of range presets, not missing format validation.

## Implementation Steps

### Task 1: Fix GoT orchestrator bugs and add property tests

- [x] Fix `cleanSubgoalItems` (`decompose.go:201`) to remap `depends_on` correctly
      instead of dropping/leaving stale references
- [x] Fix empty "Previously resolved sub-answers" output for cycle-broken and
      self-dependent subgoals (`orchestrator.go:462`)
- [x] Fix rolling memory so it drops dependent injections instead of leaking them
      forward
- [x] Write property-based tests: no dangling subgoals after cleanup, stable/idempotent
      remapping, deterministic replay against the fake-LLM test double
- [x] run project tests - must pass before next task

### Task 2: Add backup and disaster recovery for kb.db

- [x] Implement `kb backup` using SQLite `VACUUM INTO`
- [x] Add `PRAGMA integrity_check` to `kb doctor`
- [x] Document the backup/recovery procedure in README (the corpus, graph, and history
      currently live in a single SQLite file with no recovery path)
- [x] write tests for backup creation and integrity-check reporting
- [x] run project tests - must pass before next task

### Task 3: Fix broken architecture-review documentation link

- [x] Decide whether to restore `docs/architecture-review.md` from git history or remove
      the dangling references
- [x] Fix the reference in `AGENTS.md:154`
- [x] Fix the reference in `README.md:73`
- [x] write tests / doc-link checks if the project has a docs-link-check mechanism
- [x] run project tests - must pass before next task

### Task 4: Add a deterministic quality gate to CI

- [x] Build a deterministic regression suite driven by the `testkit` fake-LLM
- [x] Add golden queries with recall/precision thresholds
- [x] Add DAG-invariant checks for the GoT orchestrator
- [x] Wire the suite into `go test ./...` so it runs in normal CI, not a separate job
- [x] Favor invariant-style assertions over assertions on specific overfitted strings
- [x] write tests (the regression suite itself is the test surface for this task)
- [x] run project tests - must pass before next task

### Task 5: Add retrieval metrics and degradation visibility

- [x] Compute recall@k, latency, and cost metrics for retrieval/Ask
- [x] Add a structured `Degraded []string` field to GoT/retriever responses so
      fail-open behavior is observable instead of silent
- [x] Surface the `Degraded` field in the UI and MCP responses
- [x] Unify logging on `slog` across the affected packages
- [x] Version the MCP/web response contract so adding `Degraded` is a tracked
      compatibility change
- [x] write tests for metric computation and for the degraded-field propagation path
- [x] run project tests - must pass before next task

### Task 6: Replace in-memory BM25 with FTS5

- [x] Add an FTS5-backed candidate generator as the primary retrieval path
- [x] Remove the in-memory BM25 rebuild-on-write/rebuild-on-startup behavior it replaces
- [x] Gate the change behind an environment flag with fallback to the previous behavior
- [x] write tests comparing FTS5 candidate generation against the previous BM25 path
- [x] run project tests - must pass before next task

### Task 7: Add config hygiene tooling and presets

- [x] Add `kb config show` to dump the effective configuration (all ~46 `KB_*`
      variables: the 33 already validated in `internal/config/env.go` plus the 13 read
      directly by connectors/subsystems without central validation)
- [x] Add named presets (`fast`, `quality`) codifying the tuning learned from the
      DRAGON report
- [x] Add range validation for numeric/enum config values at startup, including for the
      13 currently-unvalidated connector/subsystem variables
- [x] write tests for `kb config show` output and for preset application
- [x] run project tests - must pass before next task

### Task 8: Speed up DRAGON benchmark iteration

- [x] Add `--persist-dir` support that skips reindexing via existing `doc_hashes`
      tracking
- [x] Add a small fixed subset of the benchmark for a one-minute sanity run
- [x] Persist metrics history across benchmark runs
- [x] write tests for the persist-dir reindex-skip logic and metrics history storage
- [x] run project tests - must pass before next task

### Task 9: Add an ANN prefilter on top of FTS5

- [x] Add entity-linking plus an ANN prefilter so cosine similarity is computed only
      within the prefiltered candidate set (O(N) → O(K))
- [x] Implement in pure Go (avoid sqlite-vec, which requires cgo)
- [x] Gate behind an environment flag with fallback; this task depends on task 6
      (FTS5) already being in place
- [x] write tests for prefilter correctness and for the fallback path
- [x] run project tests - must pass before next task

### Task 10: Improve SQLite concurrency

- [x] Profile current contention caused by `SetMaxOpenConns(1)` serializing sync and
      dashboard access
- [x] Switch to WAL mode with a read-only connection pool and a single writer
      connection
- [x] Write crash-recovery tests validating the database remains consistent after a
      crash under concurrent access
- [x] write tests for the new connection-pool behavior
- [x] run project tests - must pass before next task

### Task 11: Add an Ask response cache

- [x] Implement a cache keyed by `hash(query + corpus_version + hash(KB_*))`
- [x] Implement explicit invalidation on corpus or config change
- [x] Ensure this task lands after tasks 7 and 10, since it depends on config
      hygiene and connection-pool changes
- [x] Ensure the fail-open path benefits from the cache instead of paying the cost twice
- [x] write tests for cache hit/miss/invalidation behavior
- [x] run project tests - must pass before next task

### Task 12: Add a 👍/👎 feedback loop

- [x] Capture per-query thumbs-up/thumbs-down feedback
- [x] Feed a personal prior into RRF (reciprocal rank fusion) ranking based on feedback
- [x] Build a labeled eval set that records labeling provenance
- [x] write tests for feedback capture and RRF prior application
- [x] run project tests - must pass before next task

### Task 13: Add near-duplicate detection at indexing time

- [x] Implement simhash or minhash computation during indexing
- [x] Use it to detect and flag/skip near-duplicate documents
- [x] write tests for near-duplicate detection accuracy
- [x] run project tests - must pass before next task

### Task 14: Support embedding-model migration without full reindex

- [x] Implement `kb reindex --embed-model=X --into=shadow.db`
- [x] Add metrics comparison between the current and shadow index before cutover
- [x] write tests for the shadow-reindex flow and metrics comparison
- [x] run project tests - must pass before next task

### Task 15: Add guardrails for PII and prompt injection

- [x] Add optional PII redaction before sending content to an external LLM (opt-in, not
      default, due to overhead)
- [x] Add lightweight protection against indirect prompt injection from connector
      content included in prompts
- [x] Add token-auth/rate-limiting if the dashboard is exposed beyond loopback
- [x] write tests for PII redaction and prompt-injection mitigation
- [x] run project tests - must pass before next task

### Task 16: Verify acceptance criteria

- [ ] Verify all P0 correctness fixes (tasks 1-3) are implemented and covered by tests
- [ ] Verify all P1 measurability/scale improvements (tasks 4-8) are implemented and
      covered by tests
- [ ] Verify all P2 quality/operability improvements (tasks 9-15) are implemented and
      covered by tests
- [ ] run full project test suite
- [ ] run project linter - all issues must be fixed

## Post-Completion

*Items requiring manual intervention or deferred to a later plan - no checkboxes,
informational only*

- P3 backlog (explicitly deferred in the source plan, not part of this plan's scope):
  incremental Leiden algorithm for community detection, embedding/LLM result caches,
  and observability improvements (trace ID propagation + query plan visibility).
- Items marked "Claude Code only" in this plan were single-source findings; consider a
  follow-up review pass once implemented, since they weren't cross-validated by the
  other two reviewers.
