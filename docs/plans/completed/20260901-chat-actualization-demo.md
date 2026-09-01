# Chat Actualization Demo: Answers Change After New Chat Messages Arrive

## Overview

kb's differentiator over a static-index RAG is that it keeps knowledge
current from live chat traffic: a new Slack/Telegram/Discord/Mattermost
message that contradicts an existing fact soft-closes the old
chunks/relations instead of leaving them to rot forever
(`README.md:507-525`, "Incremental reindexing" / "Temporal knowledge
graph"). Neither the DRAGON bench (a static one-shot HF corpus, no
updates) nor `docs/plans/20260901-dragon-rag-evolution-bench.md`'s ladder
can show this — that plan's own "+Temporal" stage note says so explicitly.
This plan builds a small, fully synthetic, self-contained demo corpus
that does not exist anywhere else and is authored specifically to
exercise the actualization path.

**Scenario**: a fictional drone startup, "Аврора Роботикс". 10 seed
documents state 10 facts. 15 questions have gold answers against the seed
corpus. Then 5 Slack messages arrive, each correcting exactly one of those
10 facts (entity-overlapping with its source doc, so blast-radius
supersession fires). 10 of the 15 questions ("affected") should get a
different, updated answer after the messages land; the other 5
("control") must stay answered identically — proving the system updates
what changed and doesn't drift on what didn't.

**Why the real `slack` connector, not a hand-rolled document feed**: the
slack connector's API base URL is configurable
(`internal/connectors/chat/slack/slack.go:36`,
`cfg.Config["base_url"]`), and its `Fetch` only needs
`/conversations.history` to return
`{"ok":true,"messages":[{"user":...,"text":...,"ts":...}]}`
(`internal/connectors/chat/slack/fetch_test.go`). A small local
`net/http` fixture server serving that shape lets `kb sync` exercise the
*actual* Fetch → render → sink → indexer → supersede pipeline end to end,
fully offline and deterministic — not an approximation of it.

**Two layers of proof, not one**:
1. A deterministic regression test using the existing fake-LLM harness
   (`internal/testkit`, pattern already used in
   `internal/integration/e2e_fake_test.go`) that asserts the *mechanism*
   fires — `superseded_by` gets set on the right chunks, the right
   relation gets closed — independent of any live model's answer
   quality. This is a permanent, CI-safe regression test, not a one-off
   demo artifact.
2. A live run against the real ai-box LLM that asks the 15 questions
   before and after, to show the actual *answer text* changing — this is
   the artifact for the Habr writeup.

This plan is tasks-only; a `revmux` pass should run on Tasks 1-4 before
trusting the mechanism-level claims in the writeup, same standing
practice as the other bench plans in this repo.

## Context (from discovery)

- `docs/sources.md` — `sources.yaml` format; `slack` source needs
  `config.base_url`, `config.channels`, `secrets.token` (env-var name).
  `kb sync` reads it from `$KB_ROOT/sources.yaml`.
- `internal/connectors/chat/slack/slack.go` — `Resolve` reads
  `cfg.Config["base_url"]` (default `https://slack.com/api`); `Fetch`
  calls `GET {base}/conversations.history`.
- `internal/connectors/chat/slack/document.go:buildDocument` — one
  `connector.Document` per Slack message, `ID = "slack:<channel>:<ts>"`.
- `README.md:515-520` — blast-radius supersession: after indexing a doc,
  other docs' chunks sharing ≥1 entity get `superseded_by` set
  (`GraphStore.OverlappingChunks` + `VectorStore.SetSuperseded`); soft
  mode ranks them down, strict mode drops them from synthesis.
- `README.md:415-421` — bi-temporal relations: a new fact with the same
  `src`+predicate but different `dst` than a still-open relation closes
  the old edge (`valid_to`) instead of overwriting it.
- `internal/testkit/testkit.go` — `FakeChat`/`FakeEmbedder` for
  deterministic offline tests; `internal/integration/e2e_fake_test.go` —
  existing pattern wiring db/indexer/retriever/got.Orchestrator with the
  fake LLM, to follow for Task 3.
- `internal/bench/dragon/score.go` — the stem/order-matching answer
  scorer built for DRAGON; reuse it for grading the live run's
  before/after answers instead of writing a new matcher.
- `cmd/kb/dragon.go`, `cmd/kb/engine.go`, `cmd/kb/bench.go` — patterns for
  a new `cmd/kb` bench-style subcommand (temp/persist dir, engineBundle,
  got.Orchestrator wiring).

## Development Approach

- **Testing approach**: Regular (code first, then tests), consistent with
  the other bench-tooling plans in this repo.
- Go only, no Python.
- No invented facts left to autonomous execution: the seed corpus, the
  chat corrections, and the gold Q&A set are fully specified in
  Technical Details below — Task 1 transcribes them into fixture files
  verbatim, it does not author new content.
- No review/fix phase inside this plan — handled by a separate `revmux`
  pass after Tasks 1-4.

## Testing Strategy

- **Unit tests**: fixture server (returns the right messages, respects
  the `channel`/`oldest` query params like the real API), the
  before/after diff-and-score logic.
- **Deterministic e2e regression test** (Task 3): fake-LLM, asserts the
  supersede/bi-temporal mechanism fires — this is the one test that must
  keep passing in `go test ./...` going forward.
- The live-LLM run (Task 5) is not part of the automated suite — it's a
  plan step executed with the CLI built in Tasks 1-4, output captured
  under `docs/bench/actualization/`.

## Implementation Steps

### Task 1: Seed corpus, chat corrections, and gold Q&A fixtures

- [x] create `internal/bench/actualize/fixtures.go` (or `testdata/` +
      loader, whichever matches how `internal/bench/dragon` already
      structures fixtures) with:
      - 10 seed documents, verbatim from the "Seed corpus" list in
        Technical Details (ID, title, body)
      - 5 chat correction messages, verbatim from the "Chat corrections"
        list (channel, user, text, ts)
      - 15 Q&A entries, verbatim from the "Gold Q&A" list (question,
        `answerBefore`, `answerAfter` — equal to `answerBefore` for the 5
        control questions —, `affected bool`, and which seed doc ID each
        question targets)
- [x] write a test asserting every `answerBefore` string is a genuine
      substring/stem-match (reuse the DRAGON scorer's matcher) of its
      target seed doc's body, and every `answerAfter` for an affected
      question is a stem-match of its correction message's text (catches
      fixture typos before they cause a confusing "wrong" result later)
- [x] run `go test ./internal/bench/actualize/...` — must pass before task 2

### Task 2: Local Slack fixture server

- [x] add a small `net/http` handler (e.g.
      `internal/bench/actualize/fixtureserver.go`) serving
      `/conversations.history`: reads `channel`/`oldest` query params,
      returns the Task 1 messages for that channel as
      `{"ok":true,"messages":[...]}` in the same shape
      `internal/connectors/chat/slack/fetch_test.go` already uses,
      respecting `oldest` (only messages newer than it) so a second sync
      pass doesn't re-ingest
- [x] write tests: full fetch returns all 5 messages; fetch with `oldest`
      set to the 3rd message's `ts` returns only the later ones; unknown
      channel returns empty
- [x] run `go test ./internal/bench/actualize/...` — must pass before task 3

### Task 3: Deterministic regression test — mechanism fires

- [x] following the `internal/integration/e2e_fake_test.go` pattern
      (`testkit.NewFakeChat`/`NewFakeEmbedder`, direct
      db/indexer/retriever/got.Orchestrator wiring, no network): index
      the 10 seed docs, then feed the 5 correction messages through the
      same indexer path (as `connector.Document`s built the same way
      `buildDocument` in `internal/connectors/chat/slack/document.go`
      would build them — either call `buildDocument` directly or drive
      the real `slack.Connector` against the Task 2 fixture server, the
      latter is more faithful and preferred if it fits cleanly with the
      fake-LLM wiring)
- [x] assert: after ingesting the roadmap-correction message, the seed
      "roadmap.md" doc's chunks have `superseded_by` set to the
      correction message's document ID (`VectorStore`/`GraphStore`
      query, not answer-text inspection)
- [x] assert the same for at least one more corrected fact (e.g. budget),
      and assert an *unrelated* seed doc (e.g. "office.md", a control
      fact) has no `superseded_by` mark after all 5 corrections land
- [x] if a corrected fact maps cleanly onto a graph relation
      (same src+predicate, different dst — e.g. "AV-3 supplier" changing
      from ЭнергоЛит to PowerCell Rus), also assert
      `GraphStore.RelationsAsOf` returns the old dst before the
      correction's timestamp and the new dst after
- [x] run `go test ./...` — must pass before task 4, and must keep
      passing permanently (this is the regression test, not a one-off)

### Task 4: Live-run harness (`kb bench-actualize`)

- [x] add a `bench-actualize` subcommand to `cmd/kb` (mirrors
      `runBenchDragonCmd`'s structure: isolated persist-dir via
      `os.MkdirTemp` unless `-persist-dir` given, `newEngineBundle`,
      `got.Orchestrator` wired with default env — full pipeline, all
      features on, since this demo is end-to-end, not an ablation)
- [x] index the Task 1 seed corpus directly via `bundle.indexer.IndexDocument`
      (same as `bench-dragon` does for DRAGON texts)
- [x] ask all 15 Task 1 questions through `got.Orchestrator.Run`, save as
      the "before" answer set
- [x] start the Task 2 fixture server in-process, write a temporary
      `sources.yaml` pointing a `slack` source at it
      (`config.base_url`, `config.channels`, `secrets.token`), and run
      the sync path (reuse whatever `kb sync` calls internally — check
      `cmd/kb/sync.go` — rather than re-implementing source-loading)
      against the same persist-dir
- [x] ask the same 15 questions again, save as the "after" answer set
- [x] score both sets against `answerBefore`/`answerAfter` using the
      DRAGON scorer's matcher (`internal/bench/dragon/score.go`, or a
      thin wrapper if the types don't line up); write results (question,
      before answer, after answer, expected before/after, affected/
      control, before-score, after-score) to
      `docs/bench/actualization/run.json`
- [x] write tests for the before/after scoring and report-writing logic
      (mock the orchestrator the way existing bench tests do)
- [x] run `go test ./...` — must pass before task 5

### Task 5: Run the live demo and capture results

- [x] run `kb bench-actualize` against the real ai-box endpoint
- [x] verify: all 10 affected questions score higher against
      `answerAfter` than `answerBefore` post-correction, and all 5
      control questions score the same (or equivalently) both times
- [x] for any question that doesn't behave as expected, manually inspect
      the actual answer text — distinguish "mechanism didn't fire"
      (check Task 3's assertions still hold in this run's DB) from
      "mechanism fired but the LLM phrased the answer oddly" (scorer
      false negative) from "fixture fact is genuinely ambiguous" (fix the
      fixture, not the code)
- [x] save the run output under `docs/bench/actualization/`

### Task N-1: Verify acceptance criteria

- [x] verify Task 3's regression test passes and is wired into
      `go test ./...`
- [x] verify the Task 5 live run shows the expected before→after change
      on the affected set and stability on the control set
- [x] run full test suite (`go test ./...`)
- [x] run linter — all issues fixed

### Task 6: [Final] Write up the demo

- [x] write `docs/bench/actualization-report.md`: the scenario, the
      before/after table for all 15 questions, and explicit callouts of
      the Task 3 mechanism-level evidence (`superseded_by` marks,
      relation closures) alongside the answer-text evidence — the point
      being "this isn't just the LLM improvising differently, the
      retrieval layer actually changed what it serves"
- [x] cross-link from `README.md` (near "Incremental reindexing" /
      "Temporal knowledge graph") and from `docs/bench/dragon-report.md`
      as "see also" for the actualization angle DRAGON can't show

## Technical Details

### Seed corpus (10 docs, id / title / body)

1. `team` — "Команда Аврора Роботикс" — "Технический директор — Дмитрий
   Волков. Руководитель разработки ПО — Анна Соколова. Глава
   производства — Игорь Ковалёв."
2. `roadmap` — "Роадмап AV-3" — "Релиз дрона-курьера AV-3 запланирован на
   15 марта 2026 года."
3. `budget` — "Бюджет AV-3" — "Бюджет проекта AV-3 на 2026 год составляет
   42 млн рублей."
4. `office` — "Офис компании" — "Головной офис компании находится в
   Новосибирске, Академгородок."
5. `partners` — "Поставщики" — "Основной поставщик аккумуляторов —
   компания ЭнергоЛит."
6. `certification` — "Сертификация AV-3" — "Сертификация дрона AV-3 в
   Росавиации назначена на май 2026 года."
7. `hiring` — "Вакансии" — "Открыта вакансия инженера по авионике,
   дедлайн подачи заявок — 1 апреля 2026 года."
8. `safety` — "Технические характеристики AV-3" — "Максимальная взлётная
   масса AV-3 — 25 кг, соответствует категории лёгких БАС."
9. `investors` — "Инвесторы" — "Раунд A закрыт в декабре 2025 года,
   ведущий инвестор — фонд «СибВенчур»."
10. `warranty` — "Гарантия" — "Гарантийный срок на AV-3 — 24 месяца с
    даты поставки."

### Chat corrections (5 Slack messages, channel `C-AVRORA`)

1. ts `1780000100.000100` — "Важно: релиз AV-3 переносится с 15 марта на
   20 июня 2026 — нужно больше времени на сертификационные тесты
   батареи." (corrects `roadmap`)
2. ts `1780000200.000100` — "Обновление по бюджету: бюджет AV-3 на 2026
   год увеличен до 55 млн рублей после закрытия раунда A." (corrects
   `budget`)
3. ts `1780000300.000100` — "Кадровое: Игорь Ковалёв уходит с позиции
   главы производства, его сменяет Мария Литвинова с 1 февраля 2026
   года." (corrects `team`)
4. ts `1780000400.000100` — "Поставщик аккумуляторов меняется: с апреля
   переходим с ЭнергоЛит на PowerCell Rus из-за срывов поставок."
   (corrects `partners`)
5. ts `1780000500.000100` — "Сертификация Росавиации перенесена с мая на
   август 2026 из-за переноса релиза AV-3." (corrects `certification`)

### Gold Q&A (15 questions)

Affected (answer changes after corrections land):
1. "Когда запланирован релиз дрона AV-3?" — before: "15 марта 2026" /
   after: "20 июня 2026" (targets `roadmap`)
2. "На какую дату намечен релиз AV-3?" — same before/after (targets
   `roadmap`, second phrasing)
3. "Какой бюджет проекта AV-3 на 2026 год?" — before: "42 млн рублей" /
   after: "55 млн рублей" (targets `budget`)
4. "Сколько денег заложено на AV-3 в 2026 году?" — same before/after
   (targets `budget`, second phrasing)
5. "Кто возглавляет производство в Аврора Роботикс?" — before: "Игорь
   Ковалёв" / after: "Мария Литвинова" (targets `team`)
6. "Кто глава производства?" — same before/after (targets `team`, second
   phrasing)
7. "Кто основной поставщик аккумуляторов?" — before: "ЭнергоЛит" / after:
   "PowerCell Rus" (targets `partners`)
8. "Какая компания поставляет аккумуляторы для AV-3?" — same
   before/after (targets `partners`, second phrasing)
9. "Когда назначена сертификация AV-3 в Росавиации?" — before: "май
   2026" / after: "август 2026" (targets `certification`)
10. "На какой месяц запланирована сертификация в Росавиации?" — same
    before/after (targets `certification`, second phrasing)

Control (answer must stay identical):
11. "Где находится головной офис компании?" — "Новосибирск, Академгородок"
    (targets `office`)
12. "Какая максимальная взлётная масса AV-3?" — "25 кг" (targets `safety`)
13. "Кто ведущий инвестор раунда A?" — "фонд «СибВенчур»" (targets
    `investors`)
14. "Какой гарантийный срок на AV-3?" — "24 месяца" (targets `warranty`)
15. "До какого числа принимаются заявки на вакансию инженера по
    авионике?" — "1 апреля 2026" (targets `hiring`)

### Fixture server contract

- `GET /conversations.history?channel=C-AVRORA&oldest=<ts>` →
  `{"ok":true,"messages":[{"type":"message","user":"U-PM","text":"...","ts":"..."}]}`,
  filtered to messages with `ts > oldest` (empty `oldest` = all 5),
  ordered oldest-first to match the real API's pagination assumption in
  `internal/connectors/chat/slack/slack.go`.

## Post-Completion

**Manual verification**:
- Reading the Task 5 live answers directly (not just the scorer's
  before/after numbers) to confirm the change is a real fact update, not
  a scorer artifact — same discipline as the DRAGON plans in this repo.
- A `revmux` pass on Tasks 1-4's new code before trusting the mechanism
  claims in the writeup.

**External system updates**:
- None — the fixture server is local/in-process; the only external call
  is to the existing ai-box LLM endpoint for Task 5.
