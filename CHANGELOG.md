# Changelog

All notable changes to this project are documented in this file. The format
follows Keep a Changelog, and the project versions with Semantic Versioning.

## [1.0.0] - 2026-08-24

First public release of `kb`, a self-contained pure-Go GraphRAG knowledge base.

### Added

- Ingestion pipeline over heterogeneous sources: GitHub, GitLab, wiki, MCP
  servers, chats (Telegram, Slack, Mattermost), task trackers, Discord, RSS,
  web crawlers, and files (PDF, XLSX, JSON, SQL DDL)
- Hybrid retrieval: dense embeddings, in-memory Okapi BM25, reciprocal rank
  fusion, authority prior, and per-document caps with graph-aware expansion
- Temporal knowledge graph with entities, relations, and Louvain/Leiden
  communities, including a deterministic Go code graph (`go/ast` + `go/types`)
- Graph-of-Thoughts / LogicRAG reasoning engine with SSE progress and rolling
  memory
- Web dashboard: search and Ask history, document relations, interactive
  Cytoscape graph, integrations CRUD, reports, cleanup, and trash
- MCP server with stdio and streamable HTTP transports
- Verification subsystem: golden-graph diff, citation-integrity checks,
  contradiction detection, legal faithfulness, and closed-issue QA evals
- Deterministic fake-LLM testkit and a CI-runnable offline end-to-end test
- Apache-2.0 licensing with NOTICE, CONTRIBUTING, and SECURITY documents
- GitHub Actions CI workflow and demo setup script with dashboard screenshots

### Requirements

- Go 1.26+
- An OpenAI-compatible LLM endpoint for chat and embeddings (see `README.md` for setup)
