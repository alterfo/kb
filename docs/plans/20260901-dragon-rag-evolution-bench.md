# DRAGON RAG Evolution Bench: Native → Hybrid → Graph → Rerank → Logic → Temporal → Qualifiers

## Overview

`docs/bench/dragon-report.md` reports one number for kb's full pipeline
(75.2% on the hist corpus). It does not show which *feature* that score
depends on. This plan builds a cumulative feature ladder — start from a
plain vector-only RAG baseline, then turn on exactly one more capability
per stage — and runs the DRAGON hist benchmark at each rung, so the score
delta at each step is attributable to that one feature. This is a
narrative/diagnostic artifact (candidate material for the Habr writeup),
distinct from an official leaderboard submission.

**Stages (cumulative, each stage = previous stage + one change):**

| # | Stage | Adds | Answering path |
|---|-------|------|-----------------|
| 0 | Native | dense-only vector retrieval, single LLM call | naive (no GoT) |
| 1 | +Hybrid | `KB_HYBRID=true` (dense+BM25+RRF) | naive |
| 2 | +Graph | `KB_INDEX_GRAPH=true` (entity/relation extraction, communities, graph-aware fusion) — **requires reindex** | naive |
| 3 | +Rerank | `KB_RERANK=llm` | naive |
| 4 | +Logic | switch to `got.Orchestrator` (decompose → DAG → waves → aggregate → gaps → finalize), defaults `KB_MAX_SUBGOALS=5`/`KB_MAX_GAP_QUERIES=3` | GoT |
| 5 | +Temporal | `KB_SUPERSEDE_MODE=strict`, `KB_DETECT_CONTRADICTIONS=true` | GoT |
| 6 | +Qualifiers | `KB_QUALIFIER_FILTER=true` | GoT |

**Why a naive (non-GoT) path for stages 0-3**: `got.Orchestrator`'s
`MaxGapQueries` can't actually be forced to zero (`orchestrator.go:100`:
`if cfg.MaxGapQueries <= 0 { cfg.MaxGapQueries = 3 }`), so "cap GoT at
1 subgoal" is not a true zero-reasoning baseline — it still pays for a
decompose call and possibly gap-refinement. A real native baseline calls
`retriever.Retriever.Retrieve` (already a clean single-shot entrypoint,
independent of GoT) once and does one `Chat()` call. This isolates
"reasoning" as its own stage (4) instead of leaking into stages 0-3.

**Why only 2 indexing passes for 7 stages**: graph extraction
(`KB_INDEX_GRAPH`) is the only knob in this ladder that changes what's
*indexed*; everything else is retrieval/answering-time. So index once
without graph (serves stages 0-1) and once with graph (serves stages
2-6), and reuse each persisted index across its stages instead of
reindexing per stage.

**Time budget**: user requirement is **≤2h per run** (each indexing pass,
each stage's answer run). Full DRAGON hist corpus (542 docs) indexes at
~17s/doc (`docs/bench/dragon-report.md`) ≈ 2.5h for indexing *alone* on
the graph-enabled pass — already over budget. So this ladder runs on a
**fixed reduced subset** of the corpus (doc-limit, calibrated in Task 2)
with a **matched question set** (only questions whose gold source
documents are inside that subset — otherwise scores get diluted by
"correctly abstained because the doc isn't indexed", which isn't a
retrieval-quality signal). The same fixed doc/question subset is reused
for all 7 stages so scores are comparable across the ladder. Expect
roughly 2 (indexing) + 7 (answering) separate command invocations; total
wall-clock across the whole plan is on the order of a working day, but no
single invocation should exceed 2h — that's the constraint this plan
enforces, not "the whole ladder finishes in 2h".

This plan is tasks-only. A `revmux` pass should run on the new Go code
(Tasks 1-4) before trusting it, per this project's standing practice
(self-authored fixes near scoring/metrics logic have produced inflated
numbers before — see `docs/bench/dragon-report.md`'s bag-of-stems
precedent).

## Context (from discovery)

- `cmd/kb/dragon.go` — `bench-dragon` (index+answer) and `bench-dragon
  score`.
- `cmd/kb/bench.go:168` `benchIsolatedEnv` — currently always a throwaway
  `os.MkdirTemp`, removed on exit.
- `cmd/kb/engine.go:29` `newEngineBundle` — DB lives at
  `<PersistDir>/kb.db`; `internal/store/sqlite/db.go:314`
  `DB.ChunkCount(ctx)` is the existing "already indexed" signal.
- `internal/bench/dragon/loader.go` — `GoldQA{PublicID, TextIDs, ...}`;
  `score.go:48` confirms `GoldQA.PublicID` is the same ID space as
  `Question.ID` (submission keyed by `strconv.Itoa(PublicID)`).
  `TextIDs` is a string-encoded list (same Python-repr-list shape as the
  `set`-type `answer` field that `docs/bench/dragon-report.md`'s bug #1
  already had to parse — reuse that parser, don't rewrite it).
- `internal/bench/dragon/convert.go` — `Document.ID = strconv.Itoa(Text.ID)`,
  `Question.ID = strconv.Itoa(Question.ID)` — plain numeric strings, easy
  to intersect.
- `internal/engine/retriever/retriever.go:150` `Retriever.Retrieve(ctx,
  query, Options{K, Filter, Mode}) ([]vector.ScoredChunk, error)` — the
  single-shot retrieval entrypoint the naive path will call directly,
  bypassing `got.Orchestrator`.
- `internal/config/env.go` — knobs used by this ladder: `KB_HYBRID`,
  `KB_INDEX_GRAPH`, `KB_RERANK`, `KB_MAX_SUBGOALS`, `KB_MAX_GAP_QUERIES`,
  `KB_SUPERSEDE_MODE`, `KB_DETECT_CONTRADICTIONS`, `KB_QUALIFIER_FILTER`.
- `docs/bench/dragon-report.md` — baseline number, ~17s/doc indexing cost,
  known caveat that the DRAGON hist corpus is a static one-time import
  (no document updates/tombstones) — relevant to stage 5, see Task 9.

## Development Approach

- **Testing approach**: Regular (code first, then tests) — consistent
  with this repo's other bench-tooling work.
- Go only, no Python.
- Every code task ends with `go test ./...` passing before moving on.
- No review/fix phase inside this plan — handled by a separate `revmux`
  pass after Tasks 1-4.

## Testing Strategy

- **Unit tests**: required for the doc/question subsetting logic, the
  naive answer path, and the stage-config table — mock the LLM/retriever
  the way existing bench tests already do.
- The actual DRAGON stage runs (Tasks 5-11) are live LLM calls against
  ai-box, not part of the automated test suite; they're executed as plan
  steps using the CLI built in Tasks 1-4, with output captured under
  `docs/bench/evolution/`.

## Progress Tracking

- Mark completed items `[x]` immediately.
- Add newly discovered tasks with ➕, blockers with ⚠️.

## Implementation Steps

### Task 1: `-persist-dir` / `-force-reindex` for `kb bench-dragon`

- [ ] add `-persist-dir` flag to `runBenchDragonCmd` (`cmd/kb/dragon.go`):
      when set, use that directory instead of `benchIsolatedEnv`'s tempdir
      and do not delete it on exit; create it if missing
- [ ] when `-persist-dir` is set and `bundle.db.ChunkCount(ctx) > 0`, skip
      fetch-texts/index/BM25-refresh and go straight to answering
- [ ] add `-force-reindex` to bypass that skip and reindex anyway
- [ ] log which path was taken ("reusing persisted index at %s (%d
      chunks)" vs "indexing into %s")
- [ ] write tests in `cmd/kb/dragon_test.go`: fresh persist-dir indexes;
      second run against same dir skips indexing; `-force-reindex`
      overrides; default (no `-persist-dir`) behavior unchanged
- [ ] run `go test ./cmd/...` — must pass before task 2

### Task 2: Fixed doc/question subset with calibrated `-doc-limit`

- [ ] add `-doc-limit N` to `runBenchDragonCmd`: after fetching `texts`,
      keep only the first N (deterministic order as returned by HF)
- [ ] when `-doc-limit` is set, also fetch `dragon.FetchGoldQA` (currently
      only fetched by `bench-dragon score`) and filter `questions` to
      those whose `GoldQA.TextIDs` (parsed with the existing bracket-list
      parser from the `set`-answer scorer fix, extended/reused, not
      reimplemented) are fully contained in the kept doc IDs — this is
      the "matched question set" for a fair subset comparison
      (`-doc-limit` implies `-hist`, since gold QA only exists for hist)
- [ ] log how many docs/questions were kept after filtering
- [ ] write a small standalone calibration helper (could be a `-calibrate`
      flag or a tiny separate `kb bench-dragon calibrate` mode): indexes a
      small fixed sample (e.g. 15 docs) with `KB_INDEX_GRAPH=true`
      (worst-case per-doc cost), reports measured seconds/doc, and prints
      the largest `-doc-limit` that keeps a full graph-enabled indexing
      pass under a configurable budget (default 45 min, leaving headroom
      under the 2h/run ceiling)
- [ ] write tests for the doc-limit truncation, the TextIDs-based question
      filter (cover: fully-contained, partially-contained/excluded,
      malformed TextIDs), and the budget-from-measured-rate calculation
- [ ] run `go test ./cmd/... ./internal/bench/...` — must pass before task 3

### Task 3: Naive (non-GoT) single-call answer path

- [ ] add a small `naiveAnswer(ctx, retriever *retriever.Retriever, chat
      *llm.Client, model string, k int, query string) (answer string,
      docIDs []string, err error)` (new file, e.g.
      `internal/bench/dragon/naive.go`): call `retriever.Retrieve` once,
      build a minimal "answer using these sources" prompt from the
      returned chunks, one non-streaming `chat.Chat()` call, return the
      answer text and the source doc IDs
- [ ] wire a `-answer-mode naive|got` flag into `runBenchDragonCmd`
      (default `got`, i.e. today's behavior unchanged); `naive` skips
      constructing `got.Orchestrator` entirely and calls `naiveAnswer` per
      question instead
- [ ] write tests for `naiveAnswer` with a fake retriever/chat (covers:
      normal answer, empty retrieval result, chat error propagation)
- [ ] write a test that `-answer-mode naive` in `runBenchDragonCmd` does
      not construct a `got.Orchestrator` (e.g. via a retriever/chat fake
      that would fail the test if GoT-specific calls like decompose were
      made)
- [ ] run `go test ./...` — must pass before task 4

### Task 4: Stage-config table + budget-fitting question count

- [ ] define the 7-stage table from the Overview as data in the sweep
      tooling (reuse or extend a small runner similar to what
      `cmd/kb/bench.go` already does for the other bench command) —
      each stage: env overrides, answer-mode, whether it needs the
      graph-enabled or graph-disabled persist-dir
- [ ] add a helper that, given a measured seconds/question for the
      heaviest stage (6, all features on — calibrated the same way as
      Task 2's doc calibration, via a small pilot batch), computes the
      largest fixed question count `N` that keeps *that* stage's full
      answer run under budget (default 90 min, leaving headroom under
      2h); this `N` (capped at the Task 2 matched-question-pool size) is
      then used for **every** stage's run, so all 7 stages are scored on
      an identical question set
- [ ] write tests for the stage-config table (one test per stage verifying
      the expected env overrides) and for the budget-fitting calculation
- [ ] run `go test ./...` — must pass before task 5

### Task 5: Index Pass A (no graph) for stages 0-1

- [ ] run `kb bench-dragon -hist -doc-limit <calibrated N from Task 2,
      no-graph variant> -persist-dir docs/bench/evolution/persist-a
      -answer-mode naive` with `KB_INDEX_GRAPH=false KB_HYBRID=false` to
      build the index and confirm it completes indexing well under 2h
- [ ] confirm `docs/bench/evolution/persist-a/kb.db` has the expected
      chunk count (no entities/relations tables populated)

### Task 6: Stage 0 — Native

- [ ] run against `persist-a` (reuse, no reindex), `KB_HYBRID=false`,
      `-answer-mode naive`, fixed question count from Task 4
- [ ] score with `kb bench-dragon score`, save submission + score report
      under `docs/bench/evolution/stage0-native.json` /
      `stage0-native.score.json`
- [ ] confirm the run stayed under 2h; record actual wall time in the
      results table (Task 12)

### Task 7: Stage 1 — +Hybrid

- [ ] run against `persist-a` (reuse), `KB_HYBRID=true`, `-answer-mode
      naive`, same fixed question set
- [ ] score and save as `stage1-hybrid.*`
- [ ] record wall time

### Task 8: Index Pass B (graph on) for stages 2-6

- [ ] run `kb bench-dragon -hist -doc-limit <calibrated N, graph variant
      from Task 2> -persist-dir docs/bench/evolution/persist-b` with
      `KB_INDEX_GRAPH=true KB_HYBRID=true` — same underlying document
      subset as Pass A where possible (same `-doc-limit` value) so stages
      0-6 all evaluate the same corpus
- [ ] confirm indexing completes under 2h; if the Task 2 calibration was
      conservative and the graph pass still risks exceeding budget, lower
      `-doc-limit` for both passes and re-run Task 5 too (record as ⚠️ if
      this happens)
- [ ] confirm `docs/bench/evolution/persist-b/kb.db` has populated
      entity/relation/community tables

### Task 9: Stage 2 — +Graph

- [ ] run against `persist-b` (reuse), `-answer-mode naive`, same fixed
      question set
- [ ] score and save as `stage2-graph.*`, record wall time

### Task 10: Stage 3 — +Rerank

- [ ] run against `persist-b` (reuse), `KB_RERANK=llm`, `-answer-mode
      naive`
- [ ] score and save as `stage3-rerank.*`, record wall time

### Task 11: Stage 4 — +Logic (GoT)

- [ ] run against `persist-b` (reuse), `KB_RERANK=llm`, `-answer-mode got`
      (default `KB_MAX_SUBGOALS=5`/`KB_MAX_GAP_QUERIES=3`)
- [ ] score and save as `stage4-logic.*`, record wall time
- [ ] this is expected to be the slowest stage per question (multiple LLM
      calls per question via decompose/waves/gaps) — if the fixed
      question count from Task 4 was calibrated on this stage as
      intended, it should still fit under 2h; if not, treat as ⚠️ and
      re-derive `N` from this stage's actual measured rate

### Task 12: Stage 5 — +Temporal

- [ ] run against `persist-b` (reuse), same as stage 4 plus
      `KB_SUPERSEDE_MODE=strict KB_DETECT_CONTRADICTIONS=true`
- [ ] score and save as `stage5-temporal.*`, record wall time
- [ ] note explicitly in the results table: the DRAGON hist corpus is a
      single bulk import with no document updates/tombstones, so
      supersede logic is structurally inert here (nothing to supersede);
      only contradiction detection has any chance of changing the
      answer. A ~0 delta at this stage is an expected, valid finding —
      it says "this corpus doesn't exercise temporal features", not
      "temporal features don't work"

### Task 13: Stage 6 — +Qualifiers

- [ ] run against `persist-b` (reuse), same as stage 5 plus
      `KB_QUALIFIER_FILTER=true`
- [ ] score and save as `stage6-qualifiers.*`, record wall time

### Task N-1: Verify acceptance criteria

- [ ] verify all 7 stage score reports exist under `docs/bench/evolution/`
      with a consistent question count across stages
- [ ] verify each recorded wall-clock time is ≤2h
- [ ] verify `-persist-dir`/`-force-reindex`/`-doc-limit`/`-answer-mode`
      all have passing unit tests and don't change default `bench-dragon`
      behavior when unset
- [ ] run full test suite (`go test ./...`)
- [ ] run linter — all issues fixed

### Task 14: [Final] Write up the evolution report

- [ ] write `docs/bench/dragon-evolution-report.md`: table of stage →
      score → delta vs. previous stage → wall time, plus the doc/question
      subset size and how it was derived (calibration numbers from Tasks
      2 and 4)
- [ ] call out the stage 5 (+temporal) caveat from Task 12 explicitly, and
      any other stage where the delta looks like noise rather than signal
      given the reduced sample size
- [ ] cross-link from `docs/bench/dragon-report.md` and note this is a
      reduced-corpus diagnostic run (not the same sample as the 75.2%
      full-corpus number, not directly comparable in absolute terms —
      only the relative deltas between adjacent stages are the point)
- [ ] update `README.md`'s DRAGON bench section with a pointer to the new
      report if useful for the Habr writeup

## Technical Details

- Results directory layout: `docs/bench/evolution/{persist-a,persist-b}/`
  for the two indexes, `docs/bench/evolution/stageN-<name>.json`
  (submission) and `.score.json` (score report) per stage.
- Stage table fields: `{name, envOverrides map[string]string, answerMode,
  persistDir}`.
- Question-matching parser for `GoldQA.TextIDs` reuses the bracket-list
  regex approach already validated in the scorer's `set`-answer fix
  (`docs/bench/dragon-report.md`, bug #1) rather than a new implementation.

## Post-Completion

**Manual verification**:
- Reading the per-question diffs between adjacent stages (which questions
  flipped right/wrong) to sanity-check that a score delta reflects the
  feature and not noise — especially important given the small
  (calibration-bounded) sample size relative to the full 600-question set.
- A `revmux` pass on Tasks 1-4's new code before trusting any stage score,
  per this project's standing practice around self-authored
  scoring-adjacent changes.

**External system updates**:
- None — all runs are local against the existing ai-box LLM endpoint.
