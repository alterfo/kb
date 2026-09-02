# AGENTS.md — архитектура `kb`

Этот файл — единственный источник правды об архитектуре проекта для любого агента
(человека или LLM), работающего в этом репозитории. Он описывает **что уже построено**,
**как это устроено** и **куда добавлять новое**.

## Миссия

`kb` — универсальная graphRAG-база знаний поверх **кода, документов, тасков и чатов**:
единая точка ретрива и синтеза ответов по разнородным источникам организации/проекта.
Самодостаточный Go-прототип: единственная внешняя зависимость в рантайме — OpenAI-совместимый LLM-эндпоинт,
вся персистентность (векторы + граф знаний + метаданные) — один файл SQLite.

## Non-negotiable rules

General project rules apply; this section keeps only kb-specific rules.

- **Fail-open everywhere**, except embedding dimension — fail-loud (`ErrDimMismatch`): every step (retrieval/LLM/rerank/graph) returns a partial result + error; the caller degrades, never panics.
- **TDD.** Tests land in the same task as the code; pure algorithms get tests first. A task is done only when `go test ./...`, `go vet ./...`, `gofmt -l .` are clean.
- **No LLM, no network in unit tests.** DI seams: `EnvLookup`, `httptest.Server`, `HTTPDoer`/clock, in-memory `Sink`, fake `LLMClient`/`Embedder`/`Reranker`/`GraphStore`. Live LLM — only behind `//go:build integration` + `KB_LLM_IT=1`.

## Архитектура: слои и поток данных

```
                          ┌───────────────────────────────────────────┐
  внешние источники  ──►  │ Connector.Fetch → Document (chan)          │
  (GitHub/GitLab/Wiki/    │  internal/connector + internal/connectors/*│
   MCP/чаты/трекеры/      └───────────────────────┬───────────────────┘
   файлы: PDF/XLSX/JSON/                           │
   SQL DDL)                                        ▼
                                     internal/render: Document → markdown+YAML frontmatter
                                                     │
                                                     ▼
                          internal/sink: Sink.Write/Prune/Tombstone (FileSink|APISink|TeeSink)
                                          internal/state: sync-state.json (курсор), tombstones.json
                                                     │  файлы под KB_ROOT
                                                     ▼
                     internal/engine (индексатор): AddOrUpdateDocument / RemoveDocument / Reindex
                          │                                    │
                          ▼                                    ▼
        internal/engine/chunk (sentences+ChatChunker)   internal/graph: LLM-экстракция
                          │                              entities/relations → merge/dedup →
                          ▼                              communities (Louvain) → summaries
        internal/store/sqlite.VectorStore                        │
        (embeddings BLOB, brute-force cosine)                    ▼
        лексический индекс: SQLite FTS5 (дефолт)       internal/store/graphstore
        или internal/store/bm25 (in-memory, KB_FTS5=false)
                          │                              (entities/relations/communities в SQLite)
                          └───────────────┬───────────────────────┘
                                          ▼
                     internal/engine/retriever.Retriever (гибрид + graph-aware fusion)
                     dense multi-query + BM25 + RRF + authority prior + per-doc cap
                     → entity-linking → neighbor expansion → community context
                                          │
                                          ▼
                     internal/engine/rerank.Reranker (noop|llm|onnx, опционально)
                                          │
                                          ▼
                     internal/engine/got.Orchestrator (Graph-of-Thoughts)
                     decompose → parallel[retrieve→score_coverage→synthesize] →
                     aggregate → find_gaps → 1×refine → finalize
                                          │
                          ┌───────────────┴───────────────┐
                          ▼                                ▼
                 internal/mcp (MCP-сервер,           internal/web (дашборд,
                 stdio+HTTP, tools)                  html/template+htmx+SSE)
                 internal/engine/report              internal/governance (scan/
                 (synthesis + global-отчёты)         retention/trash/apply)
                                                     internal/integration (e2e)
```

Персистентность — **один файл** `$PERSIST_DIR/kb.db`: таблицы векторного стора
(`chunks`), графа (`entities`/`relations`/`communities`) и `kb_meta`/`corpus_version`
в одной SQLite-базе. Лексический индекс по умолчанию — SQLite FTS5-таблица над
`chunks`; `KB_FTS5=false` откатывает на legacy in-memory BM25, который полностью
перестраивается из `chunks` при инвалидации `corpus_version`.

## Карта пакетов (актуальное состояние, не план)

| Пакет | Роль | Заметки |
|---|---|---|
| `cmd/kb` | CLI-обвязка: `sync\|serve\|reindex\|doctor\|mcp\|plan\|describe\|verify` | все команды реализованы; `connectors.go` — регистрация коннекторов |
| `internal/config` | env (`Env`, `LoadEnv`) + `sources.yaml` (только имена секретных env-переменных) | |
| `internal/llm` | `Client` (конкретный тип, не интерфейс) — `Chat`, `ChatStream`, `Embed`, `Dim`; proxy-bypass по `KB_NO_PROXY` | Потребители (retriever, got, rerank) сами объявляют узкие локальные интерфейсы (`Embedder`, `ChatClient`), которым `*llm.Client` удовлетворяет — не полагайся на общий `LLMClient`/`EmbeddingClient` интерфейс, его нет |
| `internal/store/sqlite` | `VectorStore` и `GraphStore` — реализации поверх `ncruces/go-sqlite3` (pure-Go, без cgo); lifecycle-колонки `chunks` (`created_at/valid_to/replaces/superseded_by`) | **Векторный поиск — brute-force cosine по BLOB**, не sqlite-vec extension (asg017 cgo-only недоступен в pure-Go стеке); чанки soft-close вместо delete в update-пути |
| `internal/store/vector` | интерфейс `Store` (`EnsureDim/Upsert/DeleteByDoc/Query/AllForBM25/SoftCloseByDoc/SetSuperseded/ClearSupersededBy`) + `Chunk` с lifecycle-полями + `ScoredChunk` | контракт, реализация — в `store/sqlite` |
| `internal/store/graphstore` | интерфейс `Store` графа (`UpsertEntities/UpsertRelations/MatchEntities/Neighbors/UpsertCommunities/CommunitiesFor/PruneOrphans/OverlappingChunks/RefreshStaleCommunities/...`) | **живёт здесь, не в `internal/graph`** |
| `internal/store/bm25` | in-memory Okapi BM25, `[\p{L}\p{N}]+`-токенайзер | legacy-путь под `KB_FTS5=false`; дефолт — SQLite FTS5 (`internal/store/sqlite/fts5.go`), без rebuild-на-запись |
| `internal/store/history` | интерфейс `Store` (`RecordSearch/SearchHistory/SearchEntryByID/SaveAskRun/AskRuns/AskRun/MarkRunningInterrupted`) + типы `SearchEntry` (поле `Answer` — снапшот синтеза)/`AskRunEntry` | контракт, реализация — `sqlite.HistoryStore` в `store/sqlite`; таблицы `search_history`/`ask_runs` |
| `internal/engine/chunk` | `sentences`-based chunker + `ChatChunker` (thread-aware) | |
| `internal/engine/retriever` | `Retriever` (struct, не интерфейс), `retriever.New(cfg).Retrieve(ctx, query, opt)` | гибрид (dense+BM25+RRF+authority+cap) + graph-aware fusion; superseded-penalty; lazy refresh stale-коммунити |
| `internal/engine/rerank` | `Reranker` интерфейс, `Noop`/`llm`/`onnx` | |
| `internal/engine/got` | `Orchestrator`, `got.New(cfg).*` | LogicRAG-адаптивный Graph-of-Thoughts поверх `Retriever`: декомпозиция в DAG с зависимостями, топосортировка + волновой параллелизм, greedy forward pass с инъекцией ответов зависимостей, rolling memory (окно `RollingMemory`, дефолт 3), расширение DAG по gaps (макс. 1 итерация, `MaxGapQueries`); fail-open |
| `internal/graph` | LLM-экстракция сущностей/связей, merge/dedup, community detection (`gonum` Louvain), summaries, `GraphUpdater.UpdateDocument/RemoveDocument/OverlappingChunks/RefreshStaleCommunities/RecomputeCommunities` | пишет в `store/graphstore`, не сам стор; `RecomputeCommunities` — вход для ручного редактирования графа (web CRUD) |
| `internal/engine/report` | синтез ответов (synthesis) + глобальные GraphRAG-отчёты (global) | вызывается из got/ask-флоу и `generate_report` |
| `internal/connector` | интерфейс `Connector` (`Type/Resolve/Fetch`), типы `Document/Cursor/Config/AuthSpec/FetchInfo`, **интерфейс `Sink`** | `Sink`-контракт объявлен здесь; реализации — в `internal/sink` |
| `internal/connector/registry` | `map[type]Factory`, `New(type)` | точка регистрации нового коннектора |
| `internal/connectors/{github,gitlab,wiki,mcp,chat,tracker,searchapi,file,discord,blog,web}` | конкретные коннекторы | все реализованы: chat = telegram/slack/mattermost, tracker = yandex-tracker/youtrack/kaiten/weeek/trello, discord (SOCKS через `KB_SOCKS_PROXY`), blog = rss, web = sitemap/pages-краулер, file — через импортёры |
| `internal/transport` | общий HTTP-клиент: пейджеры, retry/backoff, ratelimit, ETag, no-proxy, SOCKS5 (`SOCKS5DialContext`) | переиспользуется всеми коннекторами |
| `internal/state` | `.sync-state.json` (курсор advance-on-success+rollback), `.tombstones.json` | |
| `internal/render` | `Document → markdown + YAML frontmatter` | golden-тесты в `render/testdata` |
| `internal/markdown` | HTML → Markdown (используется `rss`/`web`-коннекторами) | |
| `internal/sink` | `FileSink`/`APISink`/`TeeSink` — реализации `connector.Sink` | |
| `internal/importer/{pdf,xlsx,jsonf,sqlddl}` | файловые импортёры → `Document` | все реализованы; используются `file`-коннектором на sync |
| `internal/ingest` | драйвер-цикл ингеста, используется `cmd/kb sync` | в конце sync-батча вызывает `RefreshStaleCommunities` |
| `internal/governance` | сканирование/retention/trash/apply корпуса (`kb`-дашборд routes) | |
| `internal/mcp` | MCP-сервер (`modelcontextprotocol/go-sdk`): search, ask, get_document, list_sources, add_note, add_source, graph_query, generate_report, reindex, status; `HTTPHandler()` — `StreamableHTTPHandler` для монтирования в `kb serve`, `Tools()` — список для UI | реализован; два транспорта — stdio (`kb mcp`) и HTTP (смонтирован на `/mcp` внутри `kb serve`, не отдельный процесс) |
| `internal/web` | дашборд (`html/template`+htmx+SSE): search (+ история в SQLite, клик по записи → `/search?id=<id>` показывает сохранённый ответ без нового поиска), ask (+ история ранов, структурированный прогресс, fallback на историю после рестарта), documents (+ секция graph relationships на `/documents/view`), integrations, reports, graph (интерактивный канвас Cytoscape.js + JSON API `/graph/data` с фильтрами/пагинацией), `/mcp/info` (эндпоинт+тулы+конфиг), cleanup, trash | реализован; htmx-фрагменты для CRUD (documents edit/delete → trash, graph entities/relations, integrations `sources.yaml`); vendored JS — `htmx.min.js`, `cytoscape.min.js` (без CDN, без build-шага); **только loopback** — нет аутентификации, есть деструктивные routes |
| `internal/bench` | EnterpriseRAG-Bench: `corpus` (загрузка .txt/.json корпуса и вопросов JSONL с полем `language` у вопросов), `run` (раннер `kb bench`: пул вопросов → answers JSONL сабмита Onyx + per-type и per-language метрики recall/abstain/cited; `compare` — дельта-режим `kb bench compare <baseline> <candidate>`) | формат сабмита `{question_id, answer, document_ids}`; RU/EN-датасет — `testdata/lang-bench`; фейк-e2e без сети; live-прогон — за `//go:build integration` + `KB_LLM_IT=1` |
| `internal/verify` | golden-graph diff (`DiffGraph`), citation-existence (`CheckCitations`), contradiction-detection, `legaleval` (legal faithfulness), `qa` (Leon issue Q&A evals, `kb verify`) | fail-open; live-прогоны за `//go:build integration` + `KB_LLM_IT=1` |
| `internal/integration` | e2e-тесты: CI-запускаемый fake-LLM прогон (import→index→search→ask→graph, без LLM) + live-прогоны против LLM | fake-LLM e2e — без build-тега; live-прогоны — `//go:build integration` + `KB_LLM_IT=1` |
| `internal/testkit` | детерминированный importable fake-LLM: `FakeChat` (canned markers + dynamic citations), `FakeEmbedder` (hash→vector, L2-normalized) | используется fake-LLM e2e и unit-тестами; без сети и LLM |

## Как добавить новый коннектор

1. Пакет `internal/connectors/<name>` реализует `connector.Connector`
   (`Type() string`, `Resolve(ctx, cfg, env) error`, `Fetch(ctx, since, out) (Cursor, FetchInfo, error)`).
2. Регистрация фабрики в `internal/connector/registry` (через `init()` в пакете коннектора).
3. Секреты — только имена env-переменных в `AuthSpec`/`sources.yaml`, значения — через
   `EnvLookup` на `Resolve`.
4. Пагинация/retry/ratelimit — переиспользуй `internal/transport`, не пиши свой HTTP-слой.
5. Инкрементальность — курсор через `internal/state` (advance-on-success + rollback),
   `PruneOrphans`/tombstone только на `FullReconcile`.
6. Frontmatter полей — специфичные для сервиса ключи поверх общих
   (`source,id,url,updated_at,title`), golden-тест на рендер в `render/testdata`.
7. Тестовые оси: auth, пагинация, инкремент курсора, advance/rollback,
   frontmatter golden, tombstone, prune-only-on-full-reconcile, ratelimit/backoff
   (fake clock), fail-open.

## Как добавить новый импортёр файлов

Реализуй `internal/importer.FileImporter{Ext() string; Import(path) ([]Document, error)}`,
положи sample-фикстуру в `testdata/` с golden-выводом. Импортёр не работает с сетью и не
знает про `Sink` — только файл → `[]Document`, дальше общий путь ингеста.

## Осознанные вырезы из объёма (не пробелы)

- **ACL/права доступа** — прототип однопользовательский локальный. Есть passthrough-поле
  `visibility` во frontmatter (задел на будущее), ничем не фильтруется сейчас.
- **`ConflictResolver` как отдельная подсистема** — не строим. Решается authority prior
  (`notes/approved/` весит больше шумного чата) + обязательным цитированием источников
  по filename в синтезе ответа — пользователь видит оба факта и решает сам.
- **Полный BM25-rebuild на запись** — снят по умолчанию переходом на SQLite FTS5
  (`KB_FTS5=true` по умолчанию); legacy in-memory BM25 (`KB_FTS5=false`) остаётся с тем же
  компромиссом для отката. Community detection — lazy: записи помечают затронутые компоненты
  `stale`, Detect идёт пакетно (конец sync-батча / query throttle) по затронутым компонентам,
  без инкрементальных delta-обновлений; инкрементальный Leiden остаётся кандидатом на будущее.
- **Embedding-based blast-radius** — не строим: cross-doc инвалидация идёт по пересечению
  сущностей (≥ N, `superseded_by` + мягкий penalty, fail-open), embedding-рефайнмент
   (ловля парафраз) — после накопления статистики по precision.

## Куда смотреть за подробностями

- `docs/README.md` — индекс документации (архитектура, коннекторы, верификация, скриншоты).
- `docs/screenshots/` — дашборд-скриншоты (search, Ask, graph, MCP info).
- `CHANGELOG.md` — release notes первого публичного релиза.
- `README.md` — стек, requirements, конфигурация, команды разработки, секции верификации и скриншотов.
