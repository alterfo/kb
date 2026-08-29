# kb documentation

Index of the project documentation. Start with the README at the repository
root, then follow the links below for design rationale, evaluation, and
release-readiness material.

## Getting started

- `../README.md` — product overview, quickstart, configuration, and CLI usage
- `../CONTRIBUTING.md` — development and testing conventions
- `../SECURITY.md` — loopback/no-auth design and vulnerability reporting
- `../CHANGELOG.md` — release notes

## Architecture

- `architecture-review.md` — consolidated architectural review: module map,
  strengths, risks, recommendations, and public-release readiness checklist
- `architecture.md` — GraphRAG pipeline diagram and data flow

## Connectors and importers

- `sources.md` — `sources.yaml` format and per-connector options
- `new-connector.md` — how to add a new connector, with the required test axes

## Verification and evaluation

- `architecture-review.md` — describes the verification layer: citation
  integrity (`CheckCitations`), golden-graph diff (`DiffGraph`), contradiction
  detection, legal faithfulness, and QA eval
- `legal-gold-corpus.md` — legal gold-corpus methodology and eval metrics
- `leiden-audit-20260821.md` — independent audit of the `go-leiden` dependency
- EnterpriseRAG-Bench: `kb bench` runner, submission format and per-type metrics — see README
  ("EnterpriseRAG-Bench" section) and `plans/20260826-enterpriserag-bench-submission.md`

## Plans

- `plans/` — active and completed implementation plans
- `plans/completed/` — historical design rationale and task breakdowns
