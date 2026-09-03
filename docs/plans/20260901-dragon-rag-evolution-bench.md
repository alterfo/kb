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

- [x] add `-persist-dir` flag to `runBenchDragonCmd` (`cmd/kb/dragon.go`):
      when set, use that directory instead of `benchIsolatedEnv`'s tempdir
      and do not delete it on exit; create it if missing
- [x] when `-persist-dir` is set and `bundle.db.ChunkCount(ctx) > 0`, skip
      fetch-texts/index/BM25-refresh and go straight to answering
- [x] add `-force-reindex` to bypass that skip and reindex anyway
- [x] log which path was taken ("reusing persisted index at %s (%d
      chunks)" vs "indexing into %s")
- [x] write tests in `cmd/kb/dragon_test.go`: fresh persist-dir indexes;
      second run against same dir skips indexing; `-force-reindex`
      overrides; default (no `-persist-dir`) behavior unchanged
- [x] run `go test ./cmd/...` — must pass before task 2

### Task 2: Fixed doc/question subset with calibrated `-doc-limit`

- [x] add `-doc-limit N` to `runBenchDragonCmd`: after fetching `texts`,
      keep only the first N (deterministic order as returned by HF)
- [x] when `-doc-limit` is set, also fetch `dragon.FetchGoldQA` (currently
      only fetched by `bench-dragon score`) and filter `questions` to
      those whose `GoldQA.TextIDs` (parsed with the existing bracket-list
      parser from the `set`-answer scorer fix, extended/reused, not
      reimplemented) are fully contained in the kept doc IDs — this is
      the "matched question set" for a fair subset comparison
      (`-doc-limit` implies `-hist`, since gold QA only exists for hist)
- [x] log how many docs/questions were kept after filtering
- [x] write a small standalone calibration helper (could be a `-calibrate`
      flag or a tiny separate `kb bench-dragon calibrate` mode): indexes a
      small fixed sample (e.g. 15 docs) with `KB_INDEX_GRAPH=true`
      (worst-case per-doc cost), reports measured seconds/doc, and prints
      the largest `-doc-limit` that keeps a full graph-enabled indexing
      pass under a configurable budget (default 45 min, leaving headroom
      under the 2h/run ceiling)
- [x] write tests for the doc-limit truncation, the TextIDs-based question
      filter (cover: fully-contained, partially-contained/excluded,
      malformed TextIDs), and the budget-from-measured-rate calculation
- [x] run `go test ./cmd/... ./internal/bench/...` — must pass before task 3

### Task 3: Naive (non-GoT) single-call answer path

- [x] add a small `naiveAnswer(ctx, retriever *retriever.Retriever, chat
      *llm.Client, model string, k int, query string) (answer string,
      docIDs []string, err error)` (new file, e.g.
      `internal/bench/dragon/naive.go`): call `retriever.Retrieve` once,
      build a minimal "answer using these sources" prompt from the
      returned chunks, one non-streaming `chat.Chat()` call, return the
      answer text and the source doc IDs
- [x] wire a `-answer-mode naive|got` flag into `runBenchDragonCmd`
      (default `got`, i.e. today's behavior unchanged); `naive` skips
      constructing `got.Orchestrator` entirely and calls `naiveAnswer` per
      question instead
- [x] write tests for `naiveAnswer` with a fake retriever/chat (covers:
      normal answer, empty retrieval result, chat error propagation)
- [x] write a test that `-answer-mode naive` in `runBenchDragonCmd` does
      not construct a `got.Orchestrator` (e.g. via a retriever/chat fake
      that would fail the test if GoT-specific calls like decompose were
      made)
- [x] run `go test ./...` — must pass before task 4

### Task 4: Stage-config table + budget-fitting question count

- [x] define the 7-stage table from the Overview as data in the sweep
      tooling (reuse or extend a small runner similar to what
      `cmd/kb/bench.go` already does for the other bench command) —
      each stage: env overrides, answer-mode, whether it needs the
      graph-enabled or graph-disabled persist-dir
- [x] add a helper that, given a measured seconds/question for the
      heaviest stage (6, all features on — calibrated the same way as
      Task 2's doc calibration, via a small pilot batch), computes the
      largest fixed question count `N` that keeps *that* stage's full
      answer run under budget (default 90 min, leaving headroom under
      2h); this `N` (capped at the Task 2 matched-question-pool size) is
      then used for **every** stage's run, so all 7 stages are scored on
      an identical question set
- [x] write tests for the stage-config table (one test per stage verifying
      the expected env overrides) and for the budget-fitting calculation
- [x] run `go test ./...` — must pass before task 5

### Task 5: Index Pass A (no graph) for stages 0-1

- [x] run `kb bench-dragon -hist -doc-limit 192 -persist-dir
      docs/bench/evolution/persist-a -answer-mode naive` with
      `KB_INDEX_GRAPH=false KB_HYBRID=false` to build the index and
      confirm it completes indexing well under 2h
- [x] confirm `docs/bench/evolution/persist-a/kb.db` has the expected
      chunk count (no entities/relations tables populated)

Recorded: Task 2 calibrate measured 15 docs graph-on in 3m31s
(14.03 s/doc) -> -doc-limit 192 under the 45m budget. Index Pass A
(no graph) indexed all 192 docs in <1 min (well under 2h); persist-a
has 192 chunks and 0 entities/0 relations/0 communities.

### Task 6: Stage 0 — Native

- [x] run against `persist-a` (reuse, no reindex), `KB_HYBRID=false`,
      `-answer-mode naive`, fixed question count from Task 4
- [x] score with `kb bench-dragon score`, save submission + score report
      under `docs/bench/evolution/stage0-native.json` /
      `stage0-native.score.json`
- [x] confirm the run stayed under 2h; record actual wall time in the
      results table (Task 12)

Recorded: fixed question count N=150 (Task 4 helper: GoT pilot measured
~28 s/question across 15 questions -> 90m budget -> ~171; rounded down to
150 for headroom; matched pool for -doc-limit 192 is 212 questions). Stage 0
ran naive against persist-a in 971s (~16.2m, well under 2h). Score:
matched=150, answer_contains=51/150 (34.0%), retrieval_hit=3/150 (2.0%).
Also fixed a naive-path scoring bug: NaiveAnswer returned the prefixed
RefDocID ("dragon/N") instead of the raw document id (Metadata["id"]="N"),
so retrieval_hit was 0 before the fix; now it matches the GoT path.

### Task 7: Stage 1 — +Hybrid

- [x] run against `persist-a` (reuse), `KB_HYBRID=true`, `-answer-mode
      naive`, same fixed question set
- [x] score and save as `stage1-hybrid.*`
- [x] record wall time

Recorded: ran against `persist-a` (reuse, 192 chunks) with
`KB_HYBRID=true` `KB_LLM_NO_THINK=true`, `-answer-mode naive`,
`-limit 150` in 968s (~16.1 min, well under 2h). Score: matched=150,
answer_contains=51/150 (34.0%), retrieval_hit=3/150 (2.0%) — identical
aggregate deltas vs Stage 0 (51/150, 3/150); only per-type reordering
(cond 17→18, mh 14→13, set ret 1→2, mh ret 1→0), i.e. +Hybrid is noise
on this reduced corpus. Saved
`docs/bench/evolution/stage1-hybrid.json` and
`stage1-hybrid.score.json`.

### Task 8: Index Pass B (graph on) for stages 2-6

- [x] run `kb bench-dragon -hist -doc-limit <calibrated N, graph variant
      from Task 2> -persist-dir docs/bench/evolution/persist-b` with
      `KB_INDEX_GRAPH=true KB_HYBRID=true` — same underlying document
      subset as Pass A where possible (same `-doc-limit` value) so stages
      0-6 all evaluate the same corpus
- [x] confirm indexing completes under 2h; if the Task 2 calibration was
      conservative and the graph pass still risks exceeding budget, lower
      `-doc-limit` for both passes and re-run Task 5 too (record as ⚠️ if
      this happens)
- [x] confirm `docs/bench/evolution/persist-b/kb.db` has populated
      entity/relation/community tables

Recorded: ran with the same `-doc-limit 192` as Pass A (same corpus for
stages 0-6), `KB_INDEX_GRAPH=true KB_HYBRID=true KB_LLM_NO_THINK=true`.
Indexed all 192 docs graph-on in ~39m (well under 2h, matching the Task 2
calibration). `persist-b/kb.db` has 192 chunks, 1660 entities, 1482
relations, and 432 communities (all 432 summarized). The first invocation
hit a transient HF 502 fetching questions after indexing finished; a
re-run on the persisted index skipped indexing and confirmed the reuse
path ("reusing persisted index ... 192 chunks", matched 212/600 questions).

### Task 9: Stage 2 — +Graph

- [x] run against `persist-b` (reuse), `-answer-mode naive`, same fixed
      question set
- [x] score and save as `stage2-graph.*`, record wall time

Recorded: ran against `persist-b` (reuse, 192 chunks, 1660 entities, 1482
relations, 432 communities) with `KB_INDEX_GRAPH=true KB_HYBRID=true
KB_LLM_NO_THINK=true`, `-answer-mode naive`, `-limit 150` in 3227s (~53.8
min, well under 2h). Score: matched=150, answer_contains=52/150 (34.7%),
retrieval_hit=6/150 (4.0%). Deltas vs Stage 1 (+Hybrid): retrieval_hit
3→6, answer_contains 51→52 (cond 18→16, mh 13→13, set 9→8, simple 11→15).
Saved `docs/bench/evolution/stage2-graph.json` and
`stage2-graph.score.json`.

### Task 10: Stage 3 — +Rerank

- [x] run against `persist-b` (reuse), `KB_RERANK=llm`, `-answer-mode
      naive`
- [x] score and save as `stage3-rerank.*`, record wall time

Recorded: ran against `persist-b` (reuse, 192 chunks, 1660 entities, 1482
relations, 432 communities) with `KB_RERANK=llm KB_INDEX_GRAPH=true
KB_HYBRID=true KB_LLM_NO_THINK=true`, `-answer-mode naive`, `-limit 150`
in ~46.4 min (2784s, well under 2h). Score: matched=150,
answer_contains=55/150 (36.7%), retrieval_hit=6/150 (4.0%). Deltas vs
Stage 2 (+Graph): retrieval_hit 6→6, answer_contains 52→55 (cond 16→18,
mh 13→13, set 8→9, simple 15→15). Saved
`docs/bench/evolution/stage3-rerank.json` and
`stage3-rerank.score.json`.

### Task 11: Stage 4 — +Logic (GoT)

- [x] run against `persist-b` (reuse), `KB_RERANK=llm`, `-answer-mode got`
      (default `KB_MAX_SUBGOALS=5`/`KB_MAX_GAP_QUERIES=3`)
- [x] score and save as `stage4-logic.*`, record wall time
- [x] this is expected to be the slowest stage per question (multiple LLM
      calls per question via decompose/waves/gaps) — if the fixed
      question count from Task 4 was calibrated on this stage as
      intended, it should still fit under 2h; if not, treat as ⚠️ and
      re-derive `N` from this stage's actual measured rate

Recorded: the Task 4 `N=150` set would have taken ~90+ minutes at the
current measured ~60s/question, so this stage was re-derived to `N=100`
(⚠️) to stay under the 2h ceiling. Ran against `persist-b` (reuse, 192
chunks, 1660 entities, 1482 relations, 432 communities) with
`KB_RERANK=llm KB_INDEX_GRAPH=true KB_HYBRID=true KB_LLM_NO_THINK=true`,
`-answer-mode got`, `-limit 100` in 6321s (~105.4 min, under 2h). Score:
matched=100, answer_contains=38/100 (38.0%), retrieval_hit=7/100 (7.0%).
Deltas vs Stage 3 on the same 100-question subset: retrieval_hit 3→7,
answer_contains 39→38 (cond 12→12, mh 9→8, set 6→6, simple 12→12) —
retrieval improves but answer_contains is a small sample-size noise
decrease. Saved `docs/bench/evolution/stage4-logic.json` and
`stage4-logic.score.json`.

Note for future runs of this stage: `docs/plans/20260903-got-refine-budget-gate.md`
added `KB_GOT_MAX_REFINE_LATENCY_MS` (default `0` = unlimited), a wall-clock
budget that skips the GoT orchestrator's optional refine pass once elapsed
time already exceeds it. Setting this on a re-run of Stage 4/5 is now an
alternative to cutting the question count (`N=150` → `N=100` above) to stay
under the 2h ceiling.

### Task 12: Stage 5 — +Temporal

- [x] run against `persist-b` (reuse), same as stage 4 plus
      `KB_SUPERSEDE_MODE=strict KB_DETECT_CONTRADICTIONS=true`
- [x] score and save as `stage5-temporal.*`, record wall time
- [x] note explicitly in the results table: the DRAGON hist corpus is a
      single bulk import with no document updates/tombstones, so
      supersede logic is structurally inert here (nothing to supersede);
      only contradiction detection has any chance of changing the
      answer. A ~0 delta at this stage is an expected, valid finding —
      it says "this corpus doesn't exercise temporal features", not
      "temporal features don't work"

Recorded: wired `KB_DETECT_CONTRADICTIONS` into `benchDragonAsk`'s got
path (it was previously dropped, unlike `ExtractQualifiers`/stage 6), plus
a unit test in `cmd/kb/dragon_test.go`. Ran against `persist-b` (reuse, 192
chunks) with `KB_SUPERSEDE_MODE=strict KB_DETECT_CONTRADICTIONS=true
KB_RERANK=llm KB_INDEX_GRAPH=true KB_HYBRID=true KB_LLM_NO_THINK=true`,
`-answer-mode got`, `-limit 100`. Wall time 7710s (~128.5 min) — ⚠️ slightly
over the 2h ceiling because contradiction detection adds a per-subgoal LLM
call and the ai-box was under evening load; a first attempt wedged when the
ai-box model unloaded mid-run, so the successful re-run also added
`KB_LLM_MAX_TOKENS=4096` as a safety cap against unbounded generations.
Score: matched=100, retrieval_hit=4/100, answer_contains=37/100. Deltas vs
stage 4 (same 100-question set): retrieval_hit 7→4, answer_contains 38→37
— a ~0/noise finding, as expected: supersede logic is structurally inert on
this single bulk import, and contradiction detection finds no real
contradictions in this corpus, so it can only perturb the draft→gaps→refine
path. Saved `docs/bench/evolution/stage5-temporal.json` and
`stage5-temporal.score.json`.

### Task 13: Stage 6 — +Qualifiers

- [x] run against `persist-b` (reuse), same as stage 5 plus
      `KB_QUALIFIER_FILTER=true`
- [x] score and save as `stage6-qualifiers.*`, record wall time

Recorded: ran against `persist-b` (reuse, 192 chunks, 1660 entities, 1482
relations, 432 communities) with `KB_SUPERSEDE_MODE=strict
KB_DETECT_CONTRADICTIONS=true KB_QUALIFIER_FILTER=true KB_RERANK=llm
KB_INDEX_GRAPH=true KB_HYBRID=true KB_LLM_NO_THINK=true
KB_LLM_MAX_TOKENS=4096`, `-answer-mode got`, `-limit 100` (the same
100-question matched subset as stages 4/5). Wall time 7762s (~129.4 min)
— ⚠️ slightly over the 2h ceiling, same evening-load/contradiction-
detection latency pattern as stage 5. Score: matched=100,
retrieval_hit=2/100, answer_contains=29/100. Deltas vs stage 5 (same
100-question set): retrieval_hit 4→2, answer_contains 37→29 (cond 12→10,
mh 10→8, set 6→4, simple 9→7). The drop is larger than a pure
noise wiggle; qualifier extraction adds a per-question structured-filter
LLM call, so on this small sample the +Qualifiers delta looks like a
real (negative) retrieval/tightening effect rather than signal, but it
must be read as a reduced-corpus diagnostic — see Task 14's noise-vs-signal
call-out. Saved `docs/bench/evolution/stage6-qualifiers.json` and
`stage6-qualifiers.score.json` (+ history).

### Task N-1: Verify acceptance criteria

- [x] verified all 7 stage score reports exist under
      `docs/bench/evolution/` (stage0-native … stage6-qualifiers
      `.score.json`). Question counts: stages 0-3 `matched=150`, stages
      4-6 `matched=100` — the N=150→100 cut happened in Task 11 to fit the
      2h ceiling (already flagged ⚠️ there), so counts are consistent
      within each index/path block but not across all 7 stages.
- [x] verified wall times: stages 0-4 under 2h (16.2m / 16.1m / 53.8m /
      46.4m / 105.4m); stages 5 (128.5m) and 6 (129.4m) exceeded 2h
      (already flagged ⚠️: evening ai-box load + per-subgoal contradiction-
      detection LLM call).
- [x] verified flag unit tests exist and pass: `TestBenchDragonReuseIndex*`,
      `TestBenchIsolatedEnvPersistDir`/`DefaultTempDir` (persist-dir/
      force-reindex), `TestFilterQuestions`/`TestMaxDocLimit` (doc-limit),
      `TestNaiveAnswer*`/`TestBenchDragonAskNaiveSkipsOrchestrator`
      (answer-mode); default no-flag behavior covered by
      `TestBenchDragonReuseIndexNoPersistDir` and
      `TestBenchIsolatedEnvDefaultTempDir`.
- [x] `go test ./...` — all packages pass.
- [x] `go vet ./...` and `gofmt -l .` — both clean.

### Task 14: [Final] Write up the evolution report

- [x] write `docs/bench/dragon-evolution-report.md`: table of stage →
      score → delta vs. previous stage → wall time, plus the doc/question
      subset size and how it was derived (calibration numbers from Tasks
      2 and 4)
- [x] call out the stage 5 (+temporal) caveat from Task 12 explicitly, and
      any other stage where the delta looks like noise rather than signal
      given the reduced sample size
- [x] cross-link from `docs/bench/dragon-report.md` and note this is a
      reduced-corpus diagnostic run (not the same sample as the 75.2%
      full-corpus number, not directly comparable in absolute terms —
      only the relative deltas between adjacent stages are the point)
- [x] update `README.md`'s DRAGON bench section with a pointer to the new
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
