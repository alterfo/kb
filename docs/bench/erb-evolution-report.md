# ERB Evolution Bench: Native → Hybrid → Graph → Rerank → Logic → Temporal → Qualifiers

Status: complete. Stages 0-3 are one run each; stages 4-6 were run three times
each with an identical configuration (2026-10-04/06, ai-box, qwen3.8 +
qwen3-embedding:0.6b).

> This is a diagnostic run on a small, category-targeted EnterpriseRAG-Bench
> slice, not a second official full-corpus ERB score. Absolute numbers are not
> comparable to a full ERB leaderboard score. The dynamic-data sample is 11
> questions, so one question moves DCS by 0.09 and differences under 2–3
> questions are noise (see Reliability and Repeats).

## Why

The DRAGON feature ladder ran on a homogeneous 192-document mass-import slice,
so Hybrid, Logic, Temporal, and Qualifier deltas stayed flat or noisy. This
slice takes questions whose gold documents exercise conflicting/updated facts
(`conflicting_info`), qualifier filtering (`constrained`), and a single-document
control group (`basic`).

## Methodology

Cumulative ladder; the stage definitions are `run.EvolutionStages()`
(`internal/bench/run/stages.go`), each stage's environment set in full from the
baseline. `persist-a` is indexed without graph extraction (stages 0–1),
`persist-b` with graph extraction (stages 2–6); both are reused.

Fixed for every stage: `KB_CHUNK_SIZE=512`, `KB_CHUNK_OVERLAP=64`,
`KB_LLM_NO_THINK=true`, `KB_LLM_TIMEOUT=300s` (stage 0 ran on the default 60s, which
was not exceeded), concurrency 1, top-k 10, local ai-box endpoints.

Chunk size matters: the default 4096 × top-k 10 overflows the model's 16k
context, Ollama truncates the prompt head, and the question is lost. A first
run with the default produced `answer_contains 0/60` with off-topic answers; it
was discarded.

Scoring:
- `retrieval_hit` — any expected document ID in `document_ids` (`kb bench score`).
- `facts_coverage` — mean fraction of `answer_facts` stems found in the answer.
- `answer_contains` — gold answer phrase present. ERB gold answers are full
  sentences, so this is ~0 for every stage and carries no signal.
- **DCS (Dynamic Currency Score)** — `kb bench dynamic`, see below.

### Dynamic Currency Score

Computed on the 11 `conflicting_info` questions with a clean superseded/current
value pair (`stale.json`, curated by hand from `gold_answer`; the other 9 differ
structurally or conditionally and are skipped). Each answer's **leading
paragraph** is classified:

- `current` — names the current value (also when the stale value follows it)
- `stale` — names only the superseded value
- `hedged` — names both, stale value first
- `missing` — names neither

`currency`, `stale_leak`, `hedge`, `miss` are the shares of each class. On a
static corpus there is no "before" snapshot, so `adoption`/`stability` (used by
the actualization scenario, `docs/bench/actualization/dcs.json`, DCS 0.95) are
not measured here and DCS equals `currency`. The gold answers themselves score
DCS 1.00, which is a ceiling check, not validation: the pairs were curated from
the same gold.

## Overall results

| # | Stage | Adds | Path | retrieval_hit /60 | ctx_recall | facts | currency (DCS) | stale_leak | hedge | miss |
|---|-------|------|------|---|---|---|---|---|---|---|
| 0 | Native | dense-only retrieval, one LLM call | naive | 54 | 0.75 | 0.23 | 0.18 | 0.09 | 0.00 | 0.73 |
| 1 | +Hybrid | `KB_HYBRID=true` | naive | 57 | 0.76 | 0.20 | 0.27 | 0.09 | 0.00 | 0.64 |
| 2 | +Graph | `KB_INDEX_GRAPH=true` | naive | 57 | 0.77 | 0.23 | 0.27 | 0.18 | 0.00 | 0.55 |
| 3 | +Rerank | `KB_RERANK=llm` | naive | 57 | 0.76 | 0.21 | 0.27 | 0.27 | 0.00 | 0.45 |
| 4 | +Logic | Graph-of-Thoughts | got | 58–60 | 0.84–0.85 | 0.24 | 0.58 (0.45–0.64) | 0.06 | 0.12 | 0.24 |
| 5 | +Temporal | `KB_SUPERSEDE_MODE=strict`, `KB_DETECT_CONTRADICTIONS=true` | got | 59–60 | 0.84 | 0.21 | 0.61 (0.55–0.64) | 0.15 | 0.09 | 0.15 |
| 6 | +Qualifiers | `KB_QUALIFIER_FILTER=true` | got | 59–60 | 0.84 | 0.25 | 0.55 (0.36–0.73) | 0.12 | 0.09 | 0.24 |

Stages 0–3: single run. Stages 4–6: mean of three runs; DCS range in brackets,
`retrieval_hit` and `ctx_recall` as range, `facts`/`stale_leak`/`hedge`/`miss`
as mean.

## Repeats (stages 4–6, three identical runs each)

| Stage | DCS r1 / r2 / r3 | mean | sd | facts r1 / r2 / r3 | retrieval_hit r1 / r2 / r3 |
|---|---|---|---|---|---|
| 4 +Logic | 0.45 / 0.64 / 0.64 | 0.58 | 0.09 | 0.26 / 0.28 / 0.18 | 60 / 58 / 58 |
| 5 +Temporal | 0.64 / 0.64 / 0.55 | 0.61 | 0.04 | 0.18 / 0.23 / 0.23 | 60 / 59 / 60 |
| 6 +Qualifiers | 0.55 / 0.73 / 0.36 | 0.55 | 0.15 | 0.23 / 0.28 / 0.24 | 59 / 59 / 60 |

The first runs ranked these stages 0.45 / 0.64 / 0.55 and suggested temporal as
the best; the repeats reverse or erase that order. Run 2 and 3 files:
`docs/bench/erb-evolution/repeats/`. The per-question class table below is
from the first runs only.

## Per-category facts_coverage

| Stage | conflicting_info | constrained | basic |
|-------|------------------|-------------|-------|
| 0 Native | 0.04 | 0.20 | 0.44 |
| 1 +Hybrid | 0.05 | 0.19 | 0.36 |
| 2 +Graph | 0.06 | 0.23 | 0.41 |
| 3 +Rerank | 0.05 | 0.19 | 0.39 |
| 4 +Logic | 0.10 | 0.21 | 0.47 |
| 5 +Temporal | 0.05 | 0.19 | 0.29 |
| 6 +Qualifiers | 0.09 | 0.20 | 0.39 |

## Per-question class (11 pairs)

| q | native | hybrid | graph | rerank | logic | temporal | qualifiers |
|---|---|---|---|---|---|---|---|
| qst_0411 | missing | missing | missing | current | hedged | current | hedged |
| qst_0412 | current | current | stale | stale | current | stale | missing |
| qst_0416 | missing | missing | missing | missing | stale | current | current |
| qst_0418 | current | missing | missing | current | current | current | current |
| qst_0419 | stale | stale | stale | stale | current | current | current |
| qst_0420 | missing | missing | missing | missing | missing | missing | missing |
| qst_0421 | missing | current | current | current | missing | current | hedged |
| qst_0423 | missing | missing | current | missing | hedged | stale | missing |
| qst_0424 | missing | missing | missing | missing | missing | missing | current |
| qst_0427 | missing | missing | missing | missing | current | current | current |
| qst_0428 | missing | current | current | stale | current | current | current |

## Observations

- **Retrieval is not the bottleneck.** `retrieval_hit` is 54–60/60 at every
  stage; the failures are in answering. With the default 4096-token chunks the
  whole run was invalid; chunk size is the single largest harness effect found.
- **Naive stages (0–3) look flat on DCS (0.18 → 0.27)**, while the share of
  `missing` falls (0.73 → 0.45) and `stale_leak` rises (0.09 → 0.27): better
  retrieval and rerank surface more documents, including superseded versions,
  and without conflict handling the model sometimes answers with the old value.
- **Graph-of-Thoughts is the first step that moves currency clearly**
  (0.27 → mean 0.58 over three runs) and lifts `context_recall` to about 0.84
  and `retrieval_hit` to 58–60/60.
- **Temporal and Qualifiers are not distinguishable from Logic or from each
  other.** Means 0.58 / 0.61 / 0.55 with run-to-run ranges of 0.19, 0.09 and
  0.37; the same configuration swings by more than any difference between stages.
  The first single runs (0.45 / 0.64 / 0.55) looked like a temporal win and were
  noise. `facts_coverage` does not follow DCS either.
- **What survives the repeats:** every one of the nine GoT runs (0.36–0.73)
  has DCS at or above the best naive-stage run (0.27); the lowest GoT run, 0.36,
  is still above it. That supports "GoT helps currency" on this sample; it does
  not support ranking the stages above it. The naive stages themselves were not
  repeated, so their own variance is unmeasured.
- Qualifier filtering has no visible effect on `constrained` facts (0.21 → 0.19
  → 0.20 in the first runs); the 20 `constrained` questions are not scored on
  currency at all.

## Reliability

- 11 dynamic questions; stages 0–3 are one run, so their DCS carries unmeasured
  variance, and stages 4–6 show run-to-run sd of 0.04–0.15 (one question = 0.09).
  The supported statements are the broad pattern (native lowest, GoT stages
  above the naive stages) and the per-question table; rankings among stages 4–6
  are not supported, and no confidence interval is claimed.
- The metric classifies the leading paragraph only, so stale values that appear
  later (in "supporting facts") are ignored by design, and contamination of
  control answers by other corrections is not detected on the static corpus.
- A current-first mention counts as current even when the answer then cites a
  conflicting source (`qst_0427`, stage 5/6); this is the intended trade-off.
- Spot-checked: `qst_0419` stage 4 answers `POST /v1/capacity/migrations/start`
  correctly; `qst_0412` stage 6 says the data is absent although stage 0 found
  the 96% figure (a real regression, not a metric artefact).

## Reproduce

```sh
kb bench slice -corpus <erb corpus> -questions questions.jsonl \
  -types conflicting_info,basic,constrained -limit-per-type 20 \
  -out-corpus docs/bench/erb-evolution/corpus \
  -out-questions docs/bench/erb-evolution/questions.jsonl

KB_CHUNK_SIZE=512 KB_CHUNK_OVERLAP=64 KB_LLM_NO_THINK=true KB_LLM_TIMEOUT=300s \
  <stage env from EvolutionStages> \
  kb bench -corpus docs/bench/erb-evolution/corpus \
    -questions docs/bench/erb-evolution/questions.jsonl \
    -answer-mode <naive|got> -persist-dir <persist-a|persist-b> -out stageN.jsonl

kb bench score -questions docs/bench/erb-evolution/questions.jsonl stageN.jsonl
kb bench dynamic -questions docs/bench/erb-evolution/questions.jsonl \
  -stale docs/bench/erb-evolution/stale.json -submission stageN.jsonl
```

Artifacts: `docs/bench/erb-evolution/stage{N}-{name}.{jsonl,score.json,dcs.json}`.

## Next

- Repeat the naive stages 0–3 as well, so the naive-vs-GoT gap has variance on
  both sides.
- Grow `stale.json` beyond 11 pairs; add control questions so `stability` and
  `adoption` can be measured on ERB.
- Revmux the scorer-affecting code (`internal/bench/dynamic`) before quoting
  these numbers externally.
