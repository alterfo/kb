# GoT Orchestrator: Budget Gate on the Refine Pass

## Overview

The GoT orchestrator (`internal/engine/got`) runs decompose -> parallel
subgoal waves -> aggregate -> find_gaps -> **at most one optional refine
pass** -> finalize. The refine pass is gated today only by a coverage
quality signal (`shouldRefine`: gaps exist AND average coverage is below
`RefineCoverageThreshold`) - there is no cost or time awareness anywhere in
the orchestrator. `Run()` already accumulates cost via `runCollector` and
times itself with `start := time.Now()`, but neither is consulted mid-run;
they are only read out after everything has finished.

This is the concrete cost problem behind the DRAGON bench's Stage 4 (+Logic)
run: with the full GoT path enabled, per-question latency (~60-75s,
decompose + subgoal waves + gap-finding + a full extra refine wave +
re-aggregation) forced Task 11 of `docs/plans/20260901-dragon-rag-evolution-bench.md`
to cut the fixed question set from 150 to 100 to fit the 2h ceiling. The
refine pass is the single most expensive optional step in the pipeline (it
reruns retrieval+coverage+synthesis for every gap query, then re-aggregates
everything), so gating *that one step* on an explicit time budget - inspired
by "Budget-Constrained Agentic Large Language Models" (arXiv:2602.11541,
INTENT) - directly targets the real bottleneck without touching the rest of
the pipeline.

This plan implements the minimal version of that idea: a per-run,
opt-in-only wall-clock budget that, immediately before the refine pass would
start, checks how much time has already elapsed since `Run()` began; if
spending more time on refine would already exceed the budget, refine is
skipped and the run falls back to the already-computed draft answer (same
fail-open pattern the orchestrator already uses everywhere else). No learned
model, no cost estimation beyond the wall clock already running.

*Out of scope, deliberately deferred*: the paper's full approach (a trained
"language world model" that predicts tool/LLM-call outcomes and does
intention-aware risk-calibrated lookahead) is a separate, later effort - see
Post-Completion.

## Context (from discovery)

- **Orchestrator entry point**: `internal/engine/got/orchestrator.go` -
  `Config` (struct, line ~57), `New()` (defaults, line ~169), `Run()` (main
  pipeline, line ~229).
- **Refine gate today**: `orchestrator.go:273` `if o.shouldRefine(gaps,
  results) { ... }` - a single `if`, never a loop; `shouldRefine()` at
  line ~344 checks `len(gaps) > 0 && averageCoverage(results) <
  o.cfg.RefineCoverageThreshold`.
- **Existing cost/time tracking**: `runCollector` (line ~105) accumulates
  `metrics.Cost` across concurrent subgoal branches via
  `withRunCollector`/`runCollectorFrom` (context-scoped); `Run()` records
  `start := time.Now()` (line ~235) and computes `metrics.LatencyMS(start)`
  only at the very end (line ~300), after finalize.
- **Existing fail-open/observability pattern**: `runCollector.degraded
  []string` (line ~108) + `addDegraded(ctx, msg)` helper (line ~153) is how
  the orchestrator already records "this run took a shortcut" without
  erroring - the budget gate should use the same mechanism
  (`g.Degraded` ends up on the returned `ThoughtGraph`).
- **Injectable time/clock precedent**: `Config.Sleep func(ctx
  context.Context, d time.Duration) error` (line ~86) is already an
  injectable time-related dependency used for deterministic retry-backoff
  tests (see `internal/engine/got/retry_test.go`). This plan follows the
  same pattern with a new `Config.Now func() time.Time`.
- **Config construction call sites** (all via `got.New(cfg)`):
  `cmd/kb/dragon.go:205` (`benchDragonAsk`), `cmd/kb/bench.go:117`
  (`bench`), `cmd/kb/actualize.go:147`, `internal/web/ask.go:174`
  (`Server.startAsk`), `internal/mcp/server.go:108`. Two of these
  (`web/ask.go`, `mcp/server.go`) don't even thread today's
  `MaxSubgoals`/`MaxGapQueries` - this plan adds the new field to all 5 for
  consistency (per user decision).
- **Env var plumbing precedent**: `internal/config/env.go` -
  `MaxSubgoals`/`MaxGapQueries` fields (~line 41-42), defaults (~line
  92-93), parsing (~line 278-290); `AbstainThreshold` similar pattern
  (~line 229-234). Catalog registration precedent: `KB_LLM_TIMEOUT` in
  `internal/config/catalog.go` (~line 164, ~line 279).
- **Tests to extend**: `internal/engine/got/orchestrator_test.go` -
  `TestRunGatesRefineOnCoverage`, `TestRunExactlyOneRefine` (must keep
  passing unchanged - default budget is unlimited),
  `TestRunReportsMetricsAndDegraded` (closest existing analog for asserting
  on `g.Degraded`). `internal/engine/got/fakes_test.go` has the shared fake
  `Retriever`/`ChatClient` doubles.
- **Consumers with orchestrator-construction tests that must keep passing**:
  `internal/web/ask_test.go`, `internal/integration/e2e_fake_test.go`,
  `internal/integration/e2e_fake_errors_test.go`,
  `internal/integration/e2e_integration_test.go`,
  `internal/integration/regression_test.go`,
  `internal/bench/run/e2e_fake_test.go`, `internal/testkit/testkit_test.go`,
  `internal/verify/legaleval/it_legal_eval_test.go`.

## Development Approach

- **Testing approach**: Regular (implement, then write/extend tests).
- Complete each task fully, with passing tests, before moving to the next.
- Default behavior must not change: `MaxRefineLatencyMS` (and the env var
  that sets it) defaults to `0` = unlimited, matching today's unconditional
  refine-on-coverage behavior everywhere until a caller explicitly opts in.
- **CRITICAL: every task MUST include new/updated tests** for code changes
  in that task.
- **CRITICAL: all tests must pass before starting next task** - no
  exceptions.
- **CRITICAL: update this plan file when scope changes during
  implementation.**
- Maintain backward compatibility - existing `got.Config{...}` call sites
  that don't set the new field must behave exactly as before.

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach above).
  No UI in this codebase area, so no e2e/Playwright tests apply here -
  existing Go integration tests under `internal/integration/` stand in for
  e2e and must keep passing.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with an "additional:" prefix.
- Document issues/blockers with a "blocked:" prefix.

## Implementation Steps

### Task 1: Add injectable clock and budget field to `got.Config`

- [x] add `Now func() time.Time` to `Config` in
      `internal/engine/got/orchestrator.go` (near `Sleep`/`JitterFunc`,
      ~line 86-87), documented as the clock used for `Run()`'s start time
      and the refine-budget check; tests can override it for determinism
- [x] default `cfg.Now` to `time.Now` in `New()` (~line 191-196, alongside
      the existing `Sleep`/`JitterFunc` defaults)
- [x] add `MaxRefineLatencyMS int64` to `Config` (near
      `RefineCoverageThreshold`, ~line 91), documented as: "0 (default) =
      unlimited; otherwise, refine is skipped once `Now() - start` already
      exceeds this many milliseconds when the refine decision is made"
- [x] replace `start := time.Now()` in `Run()` (~line 235) with `start :=
      o.cfg.Now()`
- [x] write unit test confirming `New()` defaults `cfg.Now` to a
      non-nil function equivalent to `time.Now` when unset
- [x] write unit test confirming a caller-supplied `Now` function is used
      as-is (not overridden) when set
- [x] run tests - must pass before task 2

### Task 2: Implement the refine budget gate in `Run()`

- [x] add method `func (o *Orchestrator) withinRefineBudget(start
      time.Time) bool` in `orchestrator.go`: returns `true` immediately if
      `o.cfg.MaxRefineLatencyMS <= 0`; otherwise returns
      `o.cfg.Now().Sub(start).Milliseconds() < o.cfg.MaxRefineLatencyMS`
- [x] change the refine block at `orchestrator.go:273` from `if
      o.shouldRefine(gaps, results) {` to check both `shouldRefine` and
      `withinRefineBudget(start)`; when `shouldRefine` is true but the
      budget check fails, call `addDegraded(ctx,
      "refine_skipped_budget_exceeded")` and fall through with `finalAnswer
      = draft` / `refined = false` (i.e. skip the block's body entirely,
      same as if `shouldRefine` had returned false)
- [x] write unit test: with `MaxRefineLatencyMS` set and a fake `Now` that
      reports elapsed time already past the budget when the refine decision
      is evaluated, assert `g.Degraded` contains
      `"refine_skipped_budget_exceeded"`, `finalAnswer == draft` (draft
      aggregate, unrefined), and no refine-related nodes
      (`NodeRefineAggregate`) appear in the graph
- [x] write unit test: with `MaxRefineLatencyMS` set but elapsed time still
      under budget, assert refine proceeds exactly as
      `TestRunExactlyOneRefine` already expects (reuse/adapt that test's
      fixture)
- [x] write unit test: `MaxRefineLatencyMS == 0` (default) never gates
      refine regardless of elapsed time - confirms
      `TestRunGatesRefineOnCoverage`/`TestRunExactlyOneRefine` keep passing
      unmodified
- [x] run full `internal/engine/got` test suite - must pass before task 3

### Task 3: Add `KB_GOT_MAX_REFINE_LATENCY_MS` env var

- [x] add `GoTMaxRefineLatencyMS int64` field to `Env` in
      `internal/config/env.go` (near `MaxSubgoals`/`MaxGapQueries`, ~line
      41-42)
- [x] default to `0` (disabled) in the same defaulting block as
      `MaxSubgoals`/`MaxGapQueries` (~line 92-93) - i.e. explicitly leave it
      `0` rather than assigning a non-zero default, matching "disabled by
      default"
- [x] add parsing for `KB_GOT_MAX_REFINE_LATENCY_MS` alongside the existing
      int env-var parsing block (~line 278-290); unlike sibling
      MaxSubgoals/MaxGapQueries parsers (which reject `<= 0`), this field
      treats `0` as a valid, meaningful "unlimited" value, so only
      negative/malformed values are rejected
- [x] register `KB_GOT_MAX_REFINE_LATENCY_MS` in
      `internal/config/catalog.go` following the `KB_LLM_TIMEOUT` entry
      pattern (~line 164, ~line 279) - name, description ("0 = unlimited;
      refine pass in the GoT orchestrator is skipped once this many
      milliseconds have already elapsed"), default `0`
- [x] write unit test for successful parsing of a positive value
- [x] write unit test for the default (unset -> `0`)
- [x] write unit test for rejecting a negative/malformed value (matching
      existing error-handling behavior for sibling env vars)
- [x] run `internal/config` tests - must pass before task 4

### Task 4: Thread `MaxRefineLatencyMS` through all orchestrator call sites

- [x] add `MaxRefineLatencyMS: env.GoTMaxRefineLatencyMS` to the
      `got.Config{...}` literal in `cmd/kb/dragon.go` (`benchDragonGotConfig`)
- [x] add the same field to `cmd/kb/bench.go` (`runBenchCmd`)
- [x] add the same field to `cmd/kb/actualize.go` (`actualizeOrchestrator`)
- [x] add the same field to `internal/web/ask.go` (`Server.startAsk`) -
      discovered during implementation that `ask.go` builds `got.Config`
      from `s.deps` (a `web.Deps` struct), not directly from `env`, so this
      also required: adding `GoTMaxRefineLatencyMS int64` to `web.Deps`
      (`internal/web/server.go`) and setting it from `env` at both places
      `web.Deps{...}` is constructed (`cmd/kb/serve.go`)
- [x] add the same field to `internal/mcp/server.go` (`NewServer`) -
      same discovery: `mcp.Deps` (`internal/mcp/server.go`) needed the new
      field too, set from `env` at both places `mcp.Deps{...}` is
      constructed (`cmd/kb/serve.go` and `cmd/kb/mcp.go`)
- [x] write/update a test per call site (or extend an existing
      construction test where one exists, e.g. `internal/web/ask_test.go`)
      confirming the env value reaches `got.Config.MaxRefineLatencyMS` -
      only `cmd/kb/dragon.go` has a directly testable seam
      (`benchDragonGotConfig` returns `got.Config`, extended
      `TestBenchDragonGotConfigWiresMaxRefineLatencyMS` in
      `cmd/kb/dragon_test.go`); the other 4 sites build `got.Config`
      inline or via `*got.Orchestrator` (unexported `cfg` field, not
      inspectable from `package main`/other packages), matching how
      sibling fields (`RollingMemory`, `AbstainThreshold`, `MaxSubgoals`)
      are untested at those same sites today - added no new abstraction
      solely to make this one field testable there
- [x] run full project test suite (`go test ./...`) - must pass before
      task 5

### Task 5: Verify acceptance criteria

- [x] verify default behavior is unchanged: with
      `KB_GOT_MAX_REFINE_LATENCY_MS` unset, every existing orchestrator and
      integration test still passes without modification to their
      expectations (no existing test assertions were changed; 1797 tests
      pass across 64 packages)
- [x] verify the gate fires correctly end-to-end: construct an
      `Orchestrator` with a small `MaxRefineLatencyMS` and a `Now` that
      simulates slow upstream stages, confirm refine is skipped and
      `Degraded` reflects it (`TestRunSkipsRefineWhenBudgetExceeded`)
- [x] run full test suite (`go test ./...`) - 1797 passed, 64 packages
- [x] run `go vet ./...` and `gofmt -l .` - both clean
- [x] confirm no `context.Context` cancellation/timeout behavior was
      accidentally introduced (this gate is a soft skip, not a context
      deadline - `Run()`'s signature is unchanged: `func (o *Orchestrator)
      Run(ctx context.Context, query string) ThoughtGraph`, still never
      returns an error)

### Task 6: [Final] Document the new env var

- [x] add `KB_GOT_MAX_REFINE_LATENCY_MS` to whatever existing
      config/env-var documentation lists `KB_MAX_SUBGOALS` /
      `KB_MAX_GAP_QUERIES` / `KB_LLM_TIMEOUT` today (`README.md`)
- [x] add a short note to `docs/plans/20260901-dragon-rag-evolution-bench.md`'s
      Task 11 section pointing at this env var as the mechanism now
      available to keep Stage 4 (+Logic) runs under the 2h ceiling without
      cutting the question count, for future benchmark runs

## Technical Details

**New `got.Config` fields:**
```go
// Now returns the current time; overridable for deterministic tests.
// Defaults to time.Now.
Now func() time.Time

// MaxRefineLatencyMS caps elapsed time (from Run start) beyond which the
// optional refine pass is skipped even if shouldRefine() would otherwise
// trigger it. 0 (default) means unlimited - unchanged legacy behavior.
MaxRefineLatencyMS int64
```

**Gate placement** - `orchestrator.go`, replacing the existing refine block:
```go
if o.shouldRefine(gaps, results) {
    if o.withinRefineBudget(start) {
        // ... existing refine body unchanged ...
    } else {
        addDegraded(ctx, "refine_skipped_budget_exceeded")
    }
}
```

**New env var**: `KB_GOT_MAX_REFINE_LATENCY_MS` (int64, milliseconds, default
`0` = unlimited), parsed in `internal/config/env.go`, registered in
`internal/config/catalog.go`.

**Call sites to update** (`got.Config{...}` literals):
`cmd/kb/dragon.go` (`benchDragonGotConfig`), `cmd/kb/bench.go`
(`runBenchCmd`), `cmd/kb/actualize.go` (`actualizeOrchestrator`),
`internal/web/ask.go` (`Server.startAsk`), `internal/mcp/server.go`
(`NewServer`). The latter two read from a `Deps` struct rather than `env`
directly, so `GoTMaxRefineLatencyMS` also had to be added to `web.Deps`
(`internal/web/server.go`) and `mcp.Deps` (`internal/mcp/server.go`), and
set from `env` at all 3 `Deps{...}` construction sites:
`cmd/kb/serve.go` (both `mcp.Deps{}` and `web.Deps{}`) and `cmd/kb/mcp.go`
(`mcp.Deps{}`).

## Post-Completion

**Future work (explicitly out of scope for this plan)**: the full INTENT
approach from arXiv:2602.11541 trains a learned "language world model" to
simulate future tool/LLM-call outcomes and does risk-calibrated lookahead
before every tool call, not just before the one refine pass. That is a
research-grade effort (training data collection from interaction logs, a
separate intention-predictor + conditional-generator model, calibration)
disproportionate to today's need. If the simple wall-clock gate in this plan
proves useful, a follow-up phase could explore training a lightweight
predictor of "is this refine pass likely to change the final answer" from
accumulated `ThoughtGraph` run logs - deferred until there's a concrete
signal that the coverage-threshold heuristic alone isn't good enough.

**Manual verification**: none required beyond the automated test suite -
this is a backend-only config/orchestration change with no UI surface.
