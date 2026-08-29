# Legal gold-corpus and eval metrics

This document describes the curated legal gold-corpus used to evaluate `kb`
against Russian statutory law, and the metrics computed over it. It exists so
the evaluation is reproducible: anyone can extend the corpus, re-run the
harness, and compare numbers without access to the original session.

## Why a legal pilot

Russian statutory law is a demanding test bed for a temporal knowledge graph:

- a code (ГК РФ) exists in many redactions over time — the same article has
  different valid text on different dates, which exercises the bi-temporal
  `relations` model (`valid_from`/`valid_to`, close-not-overwrite);
- Plenum resolutions (Постановления Пленума ВС РФ) clarify articles without
  having their own "expiry" in the same sense — an `INTERPRETS` edge, no
  temporal validity;
- faithfulness is objectively checkable: a cited article either exists and
  was in force on the relevant date or not, and a claim either matches the
  article + clarification or contradicts it.

## Corpus layout

The gold corpus lives in `internal/importer/legalru/testdata/gold/`:

| File | Content |
|---|---|
| `gk-rf-part1.md` | Гражданский кодекс РФ, часть первая — representative section (Раздел I), including articles with real amendment history (redaction markers) |
| `plenum-25-2015.md` | Постановление Пленума ВС РФ от 23.06.2015 N 25 «О применении судами некоторых положений раздела I части первой ГК РФ» — applicable clarifications |
| `expected_graph.json` | hand-marked expected graph: entity IDs + `AMENDS`/`INTERPRETS`-style edges used by the golden-graph diff |
| `qa_pairs.json` | question/answer pairs for faithfulness eval: `question`, `expected_articles`, `expected_plenum_points`, `justification` |

Sources: text is hand-curated from public sources only — pravo.gov.ru
(официальное опубликование правовых актов) for the codex and vsrf.ru for
Plenum resolutions. No paid/closed databases (КонсультантПлюс, Гарант) are
used; the corpus is deliberately small and manually verified.

## Markdown format parsed by `legalru`

`internal/importer/legalru` parses a fixed hierarchy deterministically, with
no LLM involved:

```
# [гк-рф] Гражданский кодекс Российской Федерации
# Часть первая
## Раздел I. Общие положения
### Глава 1. Гражданское законодательство
#### Статья 1. Основные начала гражданского законодательства
(в редакции Федерального закона от 30.12.2012 N 302-ФЗ)
1. ...
```

Redaction markers are matched inline (`(в редакции ...)` / `(в ред. ...)`):
each produces an `AMENDS` contribution with `valid_from` = amendment date and
an `Action` entity per amendment law. Each article becomes one `Document`
with `kind: legal-article`, ID `code/чN/рN/глN/стN`, and frontmatter
`code`, `code_title`, `article_number`, `article_title`, `redaction_date`,
`fz_number`, `redactions` (list of `YYYY-MM-DD:FZ`).

Plenum resolutions use `## Пункт N` headings; each point becomes a document
with `kind: legal-plenum` and ID `вс-рф/пленум/пост-25/пN`. Extraction of
`INTERPRETS`/`clarifies` edges from points to articles is LLM-based
(`internal/graph/legal.go`), and deliberately carries no temporal validity.

## Eval metrics

The harness (`internal/verify/legaleval`, `Eval.Run`) runs the GoT `ask` flow
over `qa_pairs.json` and aggregates three metrics, modeled on LegalHalBench:

- **Non-Hallucinated-Statute-Rate (NHSR)** — of the statutes cited in an
  answer, the fraction that (a) resolve to a known article of the corpus and
  (b) were in force at the relevant date (`Corpus.CurrentAt`). A citation to
  a non-existent article or to a redaction that did not yet exist counts as a
  hallucination. Plenum points are exempt (they are a separate authoritative
  source, not statutes).
- **Statute-Relevance-Rate (SRR)** — of the cited statutes, the fraction the
  LLM-judge deems actually relevant to the question.
- **Legal-Claim-Truthfulness (LCT)** — the fraction of answers whose claims
  are consistent with the expected articles + Plenum clarifications, per
  LLM-judge verdict.

Metrics are reported per-answer and aggregated in `Report.Summary()`.

## Running the eval

The deterministic parts (corpus parsing, QA-pair parsing, metric aggregation,
plenum point parsing) run in the normal unit test suite with a fake judge and
no network. The live evaluation needs a real LLM and is gated behind the
`integration` build tag:

```sh
KB_LLM_IT=1 go test -tags integration ./internal/verify/legaleval/
```

The integration test loads the gold corpus, indexes it through the full
pipeline (chunking → embeddings → graph extraction → communities), runs the
GoT ask flow on the QA pairs, and prints the three metrics.

## Surrounding verification layer

The legal eval sits on top of general-purpose checks in `internal/verify`:

- `DiffGraph` — exact diff of an extracted graph against `expected_graph.json`
  (missing/extra/mismatched), the deterministic regression harness for
  extraction (also used for the synthetic corpus in
  `internal/verify/testdata/synthetic/`);
- `CitationChecker` — every `file_name`/`chunk_id` citation in a generated
  answer must exist in the retrieved context actually passed to the LLM;
- `ContradictionDetector` — LLM pass over retrieved chunks before synthesis
  that flags explicit contradictions (e.g. two redactions of one article
  retrieved without a temporal filter); fail-open and off by default in GoT.

## Extending the corpus

1. Add/update a codex or Plenum markdown file in `internal/importer/legalru/testdata/gold/`
   using the hierarchy above (keep amendment markers accurate).
2. Update `expected_graph.json` (entities + `AMENDS`/`INTERPRETS` edges) and
   add `qa_pairs.json` entries with `justification`.
3. Run `go test ./internal/importer/legalru/ ./internal/verify/...` to verify
   the corpus parses and the golden-graph diff stays clean.
4. Re-run the integration eval for the new numbers.
