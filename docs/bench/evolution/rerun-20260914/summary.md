# DRAGON evolution ladder — rerun 2026-09-14 vs. original 2026-09-01

Same methodology, same `persist-a`/`persist-b` indexes (reused, not
rebuilt), same 192-doc/212-question matched pool, same env overrides per
rung. Only the LLM answering pass was re-executed. Ran 2026-09-14 11:11Z →
19:34Z (~8.4h wall, across several restarts after the harness killed the
background process a few times mid-run; the driver script is idempotent —
completed stages were skipped on each restart, only in-flight stages
re-ran from scratch).

## Raw numbers

| Rung | N | orig retr/ans | rerun retr/ans | Δ retr | Δ ans |
|---|---|---|---|---|---|
| 0 Native | 150 | 3/51 | 6/50 | +3 | -1 |
| 1 +Hybrid | 150 | 3/51 | 3/51 | 0 | 0 |
| 2 +Graph | 150 | 6/52 | 6/55 | 0 | +3 |
| 3 +Rerank | 150 | 6/55 | 5/55 | -1 | 0 |
| 4 +Logic | 100 | 7/38 | 3/40 | -4 | +2 |
| 5 +Temporal | 100 | 4/37 | 5/37 | +1 | 0 |
| 6 +Qualifiers | 100 | 2/29 | 4/37 | +2 | +8 |

## What replicates and what doesn't

- **+Hybrid stays flat in both runs** (51→51 orig, 50→51 rerun) — the
  "hybrid does nothing on this homogeneous corpus" conclusion holds.
- **+Graph replicates directionally**: positive answer delta in both runs
  (+1 orig, +4 rerun), though the magnitude differs.
- **+Rerank's "cleanest gain" claim does NOT replicate.** Original showed
  a clean +3 answer_contains gain (52→55) attributed to rerank alone. Rerun
  shows 0 delta (55→55) — rerank added nothing this time. The original
  framing of rerank as the most reliable single-feature win is not
  supported by this second sample.
- **+Qualifiers' "regression" does NOT replicate — this is the headline
  finding.** Original run: 37→29 (-8, read as a real negative effect of
  the feature). Rerun: 37→37 (flat). The rerun shows no qualifiers penalty
  at all. This confirms what the original report already flagged as a
  risk (small, homogeneous slice, few `constrained`-type questions) — the
  -8 delta was very likely sampling/LLM noise on N=100, not a real effect
  of `KB_QUALIFIER_FILTER`.
- **+Temporal stays inert in both runs**, as expected on a static corpus
  (37→37 orig delta 4→7 vs rerun 37→37 delta... both show ~flat
  answer_contains, consistent with the mechanism having nothing to
  supersede here).
- Individual retrieval_hit counts move by 1-4 out of 100-150 in both
  directions across every rung, including rungs with *no config change
  at all* between runs (rung 1 has the same env as rung 0's baseline
  comparison) — this is the LLM-answering non-determinism the README
  caveat already warns about, now with a second concrete data point.

## Implication for the README / article claims

- The `README.md` non-determinism caveat (single snapshot, re-run can move
  counts within a few questions) is validated — and understated the
  effect for rung 6 specifically, where the swing was 8 points on N=100,
  not "a few questions."
- The narrative claim "+Rerank is the cleanest gain in the ladder" and
  "+Qualifiers is a regression" (both repeated in `dragon-evolution-report.md`
  and the Habr article draft) are **each only a single-run observation**,
  and this rerun shows one of them (qualifiers) flips entirely and the
  other (rerank) goes to zero. Recommend either: (a) explicitly label
  these as "observed on one run, not confirmed by a second," or (b) rerun
  a third time and report a range/median instead of point values, before
  publishing them as findings rather than as raw snapshot data.

## Artifacts

`docs/bench/evolution/rerun-20260914/stage{0-6}-*.json` (answers),
`.score.json` (scores), `.score.json.history.json` (history). Driver:
`docs/bench/evolution/rerun-20260914/run.sh`.
