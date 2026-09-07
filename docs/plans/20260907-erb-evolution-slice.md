# EnterpriseRAG-Bench diagnostic slice + feature-evolution ladder

## Overview

Build a small, category-targeted slice of EnterpriseRAG-Bench (ERB) and run the same
cumulative feature-evolution ladder (Native → Hybrid → Graph → Rerank → Logic → Temporal →
Qualifiers) that `docs/bench/dragon-evolution-report.md` ran on DRAGON — but on a corpus whose
questions structurally exercise graph fusion, multi-hop reasoning, temporal/supersede logic and
qualifier filtering, plus a single-doc control group.

**Problem it solves**: The DRAGON ladder ran on a 192-doc single-mass-import slice that was too
homogeneous to move Hybrid/Logic/Temporal/Qualifier deltas — code review confirmed the corpus,
not the features, produced the flat/negative results. This slice is designed so each stage's
delta is attributable to real capability rather than small-corpus noise.

**Integration**: Reuses the existing `kb bench` (ERB) harness (`cmd/kb/bench.go`), the
`internal/bench/corpus` loader, the `internal/bench/run` runner/report, and mirrors the
`internal/bench/dragon` evolution-ladder structure and scorer shape.

## Context (from discovery)

Files/components involved:
- `cmd/kb/bench.go` — ERB bench command; today hard-codes the GoT path and omits
  contradiction-detection wiring.
- `cmd/kb/dragon.go` — reference: has `-answer-mode`, `benchDragonAsk` naive path,
  `benchDragonGotConfig` with `ContradictionDetector`/`DetectContradictions`.
- `internal/bench/run/runner.go` — ERB `Runner`, `Report`, `FilterQuestions`,
  `CorpusDocumentIDs`; has recall/abstain/cited but no answer/facts scoring.
- `internal/bench/dragon/score.go` — reference scorer (`Score`, `answerContainsGold`,
  `phraseStemsPresent`, `stemSequence`) using `snowball/russian`.
- `internal/bench/dragon/stages.go` — reference cumulative stage table.
- `internal/bench/corpus/corpus.go` — `Question` (fields incl. `ExpectedDocIDs`, `GoldAnswer`,
  `AnswerFacts`, `Type`, `Language`), `LoadCorpus`, `LoadQuestions`, `parseTXT`.
- `internal/engine/retriever/retriever.go` — ANN-prefilter fail-open fallback (`queryDense`,
  ~L397-425); qualifier filter has no analogous fallback.
- `internal/engine/got/orchestrator.go` — qualifier filter merged once per question (~L257-262).

Related patterns found:
- Naive answer helper already exists: `dragon.NaiveAnswer(ctx, r, chat, model, topK, text)`.
- English snowball stemmer is vendored (`github.com/kljensen/snowball/english`, module cache
  confirmed) — parallel to the Russian one the DRAGON scorer uses.
- Two-persist-dir reuse pattern (`persist-a` no-graph, `persist-b` graph) from the DRAGON run.

Dependencies identified:
- ERB release v1.0.0 artifacts (`all_documents.zip` 1.26GB, `questions.jsonl`).
- Live ai-box endpoint for embeddings + chat (see remote-only ollama policy).

## Development Approach

- **Testing approach**: TDD (tests first) — project rule is `go test ./...`, `go vet ./...`,
  `gofmt -l .` clean before trusting anything.
- Complete each task fully before moving to the next; small, focused changes.
- **Every code task includes new/updated tests** (success + error/edge scenarios).
- **All tests pass before starting the next task.**
- No code comments (project rule); Go only.
- Maintain backward compatibility: `kb bench` default behavior (GoT) unchanged when
  `-answer-mode` is unset.

## Testing Strategy

- **Unit tests**: required per task. Table-driven where natural, matching existing bench tests.
- **No UI / e2e**: this is a CLI/library change; end-to-end validation is a live-endpoint smoke
  run captured under Post-Completion (needs ai-box + downloaded corpus, not automatable in-loop).

## Progress Tracking

- Mark completed items `[x]` immediately.
- New tasks get ➕ prefix; blockers get ⚠️ prefix.
- Keep this file in sync if scope changes.

## What Goes Where

- **Implementation Steps** (checkboxes): code, unit tests, docs edits achievable in-repo.
- **Post-Completion** (no checkboxes): corpus download, the actual ladder run against ai-box,
  scoring runs, filling the report with real numbers, revmux of scoring-affecting code.

## Implementation Steps

### Task 1: Lift NaiveAnswer to shared package and add `--answer-mode` to `kb bench`

- [x] move `NaiveAnswer` from the `dragon` package into `internal/bench/run` (e.g.
      `internal/bench/run/naive.go`) with signature `NaiveAnswer(ctx, r, chat, model, topK, text)`
- [x] update `cmd/kb/dragon.go:199` to call the relocated `runbench.NaiveAnswer`
- [x] add `-answer-mode` flag (default `got`) to `cmd/kb/bench.go` with `got|naive` validation
      mirroring `cmd/kb/dragon.go:40,52-55`
- [x] in `runBenchCmd`, branch the `Ask` closure: `naive` → `runbench.NaiveAnswer`, `got` →
      existing orchestrator path (`cmd/kb/bench.go:117-142`)
- [x] write tests for `-answer-mode` validation (valid got/naive, invalid value → exit 2)
- [x] write tests confirming `NaiveAnswer` relocation compiles and behaves at both call sites
      (fake retriever + fake chat, success + retriever-error → empty answer)
- [x] run `go test ./... && go vet ./... && gofmt -l .` — must pass before next task

### Task 2: Wire contradiction detection into `kb bench`'s GoT config

- [ ] add `ContradictionDetector: verify.NewContradictionDetector(chat, env.LLMModel)` and
      `DetectContradictions: env.DetectContradictions` to the `got.New(...)` config in
      `cmd/kb/bench.go:117-128` (parity with `cmd/kb/dragon.go:226-227`), keeping existing
      `MaxSubgoals`/`MaxGapQueries`
- [ ] write a test asserting the bench GoT config passes contradiction-detection through when
      `KB_DETECT_CONTRADICTIONS=true` (e.g. a fake detector observing it is invoked)
- [ ] run `go test ./... && go vet ./... && gofmt -l .` — must pass before next task

### Task 3: Corpus-slice subcommand `kb bench slice`

- [ ] add a `slice` subcommand dispatch in `runBenchCmd` (alongside the existing `compare`
      branch at `cmd/kb/bench.go:20`)
- [ ] implement `runBenchSliceCmd`: flags `-corpus`, `-questions`, `-types` (reuse `csvSet`),
      `-out-corpus`, `-out-questions`, optional `-limit-per-type`
- [ ] load questions, filter via `runbench.FilterQuestions` (`internal/bench/run/runner.go:99`),
      union their `ExpectedDocIDs`, copy only matching `dsid_*.txt` files into `-out-corpus`
      preserving the `<source_type>/` layout `corpus.parseTXT` requires
- [ ] write the filtered questions to `-out-questions`; report copied-doc count and any
      `ExpectedDocIDs` with no on-disk match
- [ ] write tests: fake corpus tree + questions.jsonl → only referenced docs copied, source-type
      layout preserved, missing-doc reported, `-types` filter respected
- [ ] write tests for edge cases (no matching questions → empty slice + warning; duplicate doc
      ids deduped)
- [ ] run `go test ./... && go vet ./... && gofmt -l .` — must pass before next task

### Task 4: ERB facts-based scorer and `kb bench score`

- [ ] add `internal/bench/run/score.go`: port `stemSequence`/`phraseStemsPresent`/
      `answerContainsGold` structure from `internal/bench/dragon/score.go:96-148`, using
      `snowball/english` instead of `snowball/russian`
- [ ] compute per-type `answer_contains_gold` (vs `GoldAnswer`), facts coverage (fraction of
      `AnswerFacts` stems present in the answer), and retrieval-hit (vs `ExpectedDocIDs`); emit
      a `ScoreReport` shaped parallel to DRAGON's
- [ ] add a `score` subcommand to `runBenchCmd`: read submission JSONL + questions.jsonl, write
      `<name>.score.json`, append to `.history.json`
- [ ] write tests for the scorer on EN gold pairs: exact-contains hit/miss, partial facts
      fraction, set-style bracket gold, empty gold → no hit (mirror DRAGON scorer tests)
- [ ] write tests for the `score` subcommand wiring (submission + questions → score.json fields)
- [ ] run `go test ./... && go vet ./... && gofmt -l .` — must pass before next task

### Task 5: Qualifier-filter fail-open fix

- [ ] in `internal/engine/retriever/retriever.go`, when a retrieval leg returns empty because of
      the qualifier-derived `opt.Filter`, retry that leg unfiltered — mirroring the ANN-prefilter
      fallback pattern (`queryDense`, ~L397-425) rather than returning an empty rank list
- [ ] ensure the fallback triggers only for qualifier-filter emptiness, not for a legitimately
      empty corpus/leg (guard so behavior is unchanged when the filter is inactive)
- [ ] write tests: leg empty under qualifier filter → unfiltered retry returns candidates; no
      filter → unchanged; empty-corpus → still empty (no spurious retry)
- [ ] run `go test ./... && go vet ./... && gofmt -l .` — must pass before next task

### Task 6: Verify acceptance criteria

- [ ] verify `kb bench -answer-mode naive|got`, `kb bench slice`, and `kb bench score` all parse
      and dispatch correctly (help/exit codes)
- [ ] verify `kb bench` default (no `-answer-mode`) still runs GoT unchanged
- [ ] run full unit suite `go test ./...`
- [ ] run `go vet ./...` and `gofmt -l .` — no issues
- [ ] confirm no code comments were introduced (project rule)

### Task 7: [Final] Documentation

- [ ] add a `docs/bench/erb-evolution-report.md` skeleton mirroring
      `docs/bench/dragon-evolution-report.md` (methodology, slice construction, empty per-stage
      + per-category tables to be filled from the real run in Post-Completion)
- [ ] add a README link to the ERB evolution report next to the DRAGON one (`README.md:347-351`)
      and document the new `kb bench` subcommands/flags (`-answer-mode`, `slice`, `score`)

## Technical Details

- Target categories (counts from `docs/plans/20260903-enterpriserag-bench-run.md:38-42`):
  `conflicting_info` (20×2, temporal/graph), `completeness` (20×4+, graph/logic),
  `project_related` (40×2-4+, graph/logic), `constrained` (30×1-2, qualifiers), plus a
  `basic`+`semantic` single-doc control (~20-30). ≈130-140 questions, few-hundred gold docs.
- Ladder stages (env overrides parallel to `internal/bench/dragon/stages.go:24-99`):
  0 native (naive, persist-a, `KB_INDEX_GRAPH=false KB_HYBRID=false`), 1 +hybrid (naive,
  persist-a, `KB_HYBRID=true`), 2 +graph (naive, persist-b, `KB_INDEX_GRAPH=true`), 3 +rerank
  (naive, persist-b, `KB_RERANK=llm`), 4 +logic (got, persist-b), 5 +temporal (got, persist-b,
  `KB_SUPERSEDE_MODE=strict KB_DETECT_CONTRADICTIONS=true`), 6 +qualifiers (got, persist-b,
  `KB_QUALIFIER_FILTER=true`). All with `KB_LLM_NO_THINK=true`, concurrency 3, ai-box endpoints.
- Artifacts under `docs/bench/erb-evolution/`: `persist-a/`, `persist-b/`,
  `stage{N}-{name}.{json,score.json}`. The `.log/.pid/.start/.end/.exit` runner sidecars are
  already gitignored.
- Backward compatibility: `-answer-mode` defaults to `got`; existing callers unaffected.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes.*

**Data prep** (transient full-corpus download, ~4GB; prefer ai-box if Mac disk is tight):
- Download ERB v1.0.0 `all_documents.zip` + `questions.jsonl`; unzip to a temp corpus tree.
- Run `kb bench slice` to produce `docs/bench/erb-evolution/{corpus,questions.jsonl}`.
- Delete the temp full corpus + zip.

**Ladder run + scoring** (live ai-box endpoint required):
- Index persist-a (no graph) and persist-b (graph) once each; run the 7 stages via `kb bench`
  with the env table above, keeping each stage < 2h (project ceiling).
- Score each stage with `kb bench score`; fill `docs/bench/erb-evolution-report.md` with real
  per-stage / per-category numbers and per-stage observations contrasting each delta with the
  DRAGON-slice result.

**Verification / trust**:
- revmux the scoring-affecting code (Task 4 scorer, Task 5 qualifier fail-open) before trusting
  any stage numbers — prior precedent of inflated self-authored scores.
- Manual spot-check of 5-10 answers against source documents.

**Open items**:
- Category counts are from the plan doc, not yet verified against the real `questions.jsonl`;
  adjust the control-group size after `kb bench slice` reports actual per-type counts.
- If Task 5 is deferred, stage 6 may again read as a regression rather than a capability.
