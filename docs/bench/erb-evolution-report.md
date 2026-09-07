# ERB Evolution Bench: Native → Hybrid → Graph → Rerank → Logic → Temporal → Qualifiers

Status: methodology and empty result tables. Fill the numbers in Post-Completion
after the live ai-box run.

> This is a diagnostic run on a small, category-targeted EnterpriseRAG-Bench
> slice, not a second official full-corpus ERB score. Absolute numbers are not
> comparable to a full ERB leaderboard score. Compare only the deltas between
> adjacent stages.

## Why

The DRAGON feature ladder ran on a homogeneous 192-document mass-import slice,
so Hybrid, Logic, Temporal, and Qualifier deltas stayed flat or noisy. This
slice selects questions whose gold documents structurally exercise graph
fusion, multi-hop reasoning, temporal/supersede logic, and qualifier filtering,
plus a single-document control group, so each stage delta is attributable to a
real capability.

## Methodology

Cumulative ladder: each stage is the previous stage plus exactly one feature.
`persist-a` is indexed without graph extraction (stages 0–1) and `persist-b` is
indexed with graph extraction (stages 2–6); both indexes are reused, not
rebuilt per stage. All stages set `KB_LLM_NO_THINK=true` and run with
concurrency 3 against the ai-box endpoint.

Scoring uses the ERB gold fields:
- `retrieval_hit` — any expected document ID appears in `document_ids`.
- `answer_contains_gold` — the English gold answer stems are present in the model answer.
- `avg_facts_coverage` — mean fraction of `answer_facts` stems present in the model answer.

## Slice construction

Target counts are from the 2026-09-03 ERB run plan and are re-checked after
`kb bench slice` reports actual per-type counts.

- `conflicting_info` (20×2, temporal/graph)
- `completeness` (20×4+, graph/logic)
- `project_related` (40×2-4+, graph/logic)
- `constrained` (30×1-2, qualifiers)
- `basic` + `semantic` single-doc control (~20–30)
- Total ≈130–140 questions, few hundred gold documents

The slice is produced with:

```sh
./bin/kb bench slice \
  --corpus /path/to/unzipped/corpus \
  --questions questions.jsonl \
  --types basic,semantic,conflicting_info,completeness,project_related,constrained \
  --out-corpus docs/bench/erb-evolution/corpus \
  --out-questions docs/bench/erb-evolution/questions.jsonl
```

## Overall results

| # | Stage | Adds | Path | N | retrieval_hit | answer_contains | avg_facts | Δ retrieval_hit | Δ answer_contains | Wall time |
|---|-------|------|------|---|---------------|-----------------|-----------|-----------------|-------------------|-----------|
| 0 | Native | dense-only retrieval, one LLM call | naive | TBD | TBD | TBD | TBD | — | — | TBD |
| 1 | +Hybrid | `KB_HYBRID=true` | naive | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| 2 | +Graph | `KB_INDEX_GRAPH=true` | naive | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| 3 | +Rerank | `KB_RERANK=llm` | naive | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| 4 | +Logic | Graph-of-Thoughts | got | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| 5 | +Temporal | `KB_SUPERSEDE_MODE=strict`, `KB_DETECT_CONTRADICTIONS=true` | got | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| 6 | +Qualifiers | `KB_QUALIFIER_FILTER=true` | got | TBD | TBD | TBD | TBD | TBD | TBD | TBD |

## Per-category results (answer_contains)

| Stage | conflicting_info | completeness | project_related | constrained | basic | semantic |
|-------|------------------|--------------|-----------------|-------------|-------|----------|
| 0 Native | TBD | TBD | TBD | TBD | TBD | TBD |
| 1 +Hybrid | TBD | TBD | TBD | TBD | TBD | TBD |
| 2 +Graph | TBD | TBD | TBD | TBD | TBD | TBD |
| 3 +Rerank | TBD | TBD | TBD | TBD | TBD | TBD |
| 4 +Logic | TBD | TBD | TBD | TBD | TBD | TBD |
| 5 +Temporal | TBD | TBD | TBD | TBD | TBD | TBD |
| 6 +Qualifiers | TBD | TBD | TBD | TBD | TBD | TBD |

## Observations

Fill per-stage observations here after scoring, contrasting each delta with the
corresponding DRAGON-slice result.

- +Hybrid: TBD
- +Graph: TBD
- +Rerank: TBD
- +Logic: TBD
- +Temporal: TBD
- +Qualifiers: TBD

## Post-completion

- Verify actual category counts after `kb bench slice`.
- Index `persist-a` and `persist-b` once each.
- Run the seven stages via `kb bench` with the env overrides above.
- Score each submission with `kb bench score`.
- Fill this report with real numbers and observations.
- Revmux the scoring-affecting code and spot-check 5–10 answers before trusting the numbers.
