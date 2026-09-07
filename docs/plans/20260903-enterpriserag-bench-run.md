# EnterpriseRAG-Bench: официальный leaderboard-прогон kb

## Overview

Прогнать [EnterpriseRAG-Bench](https://github.com/onyx-dot-app/EnterpriseRAG-Bench)
(Onyx, ~512K корпоративных документов "Redwood Inference", 500 вопросов, 10
категорий) через полный пайплайн kb и подать **официальный leaderboard-сабмишен**
(HF Space `onyx-dot-app/EnterpriseRAG-Bench-Leaderboard`): письмо
joachim@onyx.app + `answers.jsonl` в формате сабмита + reproducibility guide
(kb — open source, поэтому сабмишен требует публичный гайд по воспроизведению,
а не эндпоинт).

Лидерборд оценивает через GPT-5.4 (judge): `Overall = mean(correct ×
completeness)`, плюс Document Recall и **Invalid Extra Docs** (штраф за лишние
нерелевантные доки в `document_ids` — kb уже честно цитирует только
ретривнутое, но субмит должен это сохранить).

## Что уже проверено (факты, не оценки)

- **Форматы совместимы на 100%**: корпус — zip-слайсы с `dsid_<uuid>__<slug>.txt`
  внутри `<source_type>/` (ровно формат `corpus.parseTXT`); вопросы — схема
  `corpus.Question` поле-в-поле (`question_id/question_type/source_types/
  question/expected_doc_ids/gold_answer/answer_facts`); сабмишен
  `{question_id, answer, document_ids}` — формат `runbench.Answer`.
  Проверено на реальных артефактах релиза v1.0.0.
- **Эмбеддинг-скорость** (batch=64, текст ~512 токенов, замер 2026-09-03):
  ai-box `qwen3-embedding:0.6b` — **46 чанков/с** (под нагрузкой stage5-прогона),
  локальный Mac — **37 чанков/с** одним потоком (8 ядер → 3-4 потока).
- **Корпус**: 511 962 дока ≈ 2.8GB текста; средний док 5-7KB (≈1.3-1.7K токенов)
  → при `KB_CHUNK_SIZE=4096` это ~1 чанк на док, всего **≈600-650K чанков**.
- **Проблема текущего индексационного пути**: `kb bench` эмбеддит
  последовательно, один HTTP-вызов на документ → 512K запросов × ~150-200мс
  оверхед ≈ **25-30ч**. Нужен батчевый путь (Task 4).
- **Ретривал на 512K**: brute-force `Query` (полный скан BLOB) не выживет;
  в kb уже есть `KB_ANN_PREFILTER` (FTS5 → кандидаты → `QueryCandidates` O(K))
  с fail-open fallback — это путь для масштаба, но на таком объёме он ещё не
  проверен → Task 5.
- **Категории вопросов** (структура golden-доков измерена по questions.jsonl):
  basic 175 (1 док), semantic 125 (1), intra_document 40 (1), project_related
  40 (2-4+), constrained 30 (1-2), conflicting_info 20 (2), completeness 20
  (4+), miscellaneous 20 (1), high_level 10 (0 — без golden-доков),
  info_not_found 20 (0 — корректная абстенция).

## Hardware-гейт (⚠️ решить до Task 3)

| Ресурс | Требование | Факт | Вердикт |
|---|---|---|---|
| Диск | ~14GB (архив 1.3 + корпус 3 + DB ≈8-10) | Mac: **12GB свободно** | ❌ не влезает |
| RAM | 16GB (LoadCorpus держит 512K доков ≈4GB + SQLite) | 16GB | ⚠️ впритык, ок |
| CPU/эмбеддер | 600K чанков ≈ 1.5-2ч | Mac 8 ядер / ai-box GPU | ✅ |
| Чат-LLM | 500 вопросов ≈ 2-3ч | ai-box qwen3.8 27B | ✅ |

Варианты решения диска (выбрать один):
- внешний SSD (самый простой — корпус+DB+answers живут на нём);
- расчистить ≥25GB на Mac (кандидаты: ~17GB локальных моделей ollama —
  qwen3.8 локально не нужен, он на ai-box);
- доступ к ai-box (SSH сейчас не настроен: "Too many authentication failures",
  ключей нет — нужен от владельца; тогда вся тяжёлая часть живёт там).

## Time budget (после hardware-гейта)

| Шаг | Оценка |
|---|---|
| Скачать `all_documents.zip` 1.26GB (curl с `-C -`, GitHub тротлит) | 15-30 мин |
| Распаковать в `<corpus>/<source>/*.txt` | 5 мин |
| Индексация 600-650K чанков (батчевый путь, 3-4 потока) | 1.5-2.5ч |
| Scale-тест ретривала на ~100K доков (латентность + ANNPrefilter) | 30 мин |
| Ответы на 500 вопросов (per-type naive/GoT, конк 3) | 2-3ч |
| Локальный прокси-скоринг + отчёт | 30 мин |
| Репро-гайд + письмо в Onyx | 1ч |

Ни один отдельный прогон не должен превышать 2ч (правило проекта) — ответы
при этом идут одним прогоном ~2-3ч; при необходимости делить на батчи по типам
и мёржить `answers.jsonl`.

## Implementation Steps

### Task 1: Решить hardware-гейт

- [ ] выбрать носитель: внешний SSD ИЛИ расчистка 25GB ИЛИ ssh-доступ на ai-box
- [ ] проверить место и скорость (`dd`-замер на SSD, если внешний)
- [ ] если ai-box: `df -h`, `free -h`, настроить ключ; перенести на него
      `kb` (Linux-сборка, `GOOS=linux go build`)

### Task 2: Заготовить корпус

- [ ] скачать `all_documents.zip` (+ `questions.jsonl` из релиза v1.0.0)
- [ ] распаковать: `unzip all_documents.zip -d <corpus>` (структура
      `<corpus>/<source_type>/dsid_*.txt` — уже валидна для `corpus.LoadCorpus`)
- [ ] sanity: `find <corpus> -type f | wc -l` ≈ 512K; проверить, что нет
      файлов вне source-директорий (иначе loader их молча пропустит —
      `sourceType(rel) == ""`)

### Task 3: Чистые пилот-замеры (после завершения stage5-temporal прогона)

- [ ] пере-замерить эмбеддинг ai-box/локально без контенции (stage5 сейчас
      занимает ai-box чатом и локальный ollama query-эмбеддингами)
- [ ] прогнать индексацию слайса (189-10K доков) текущим путём `kb bench` —
      получить честное s/док и s/чанк (замеры выше сделаны под нагрузкой)
- [ ] замерить латентность `Query` на 10K-доковом индексе и экстраполировать
      (или сразу перейти к Task 5 на 100K)

### Task 4: Батчевый индекс-путь для `kb bench` (код)

- [ ] `cmd/kb/bench.go` (или `internal/bench`): bulk-embed worker pool —
      очередь чанков → `llm.Client.Embed` пакетами по 32-64 текста →
      вставка в store; N worker'ов (3-4), единый порядок вставки/транзакций
      как в текущем `Indexer.IndexDocument` (chunk + doc_hashes + FTS5)
- [ ] fallback: если bulk-путь недоступен (например, кастомный Embedder) —
      старый по-dоковый путь; fail-open как везде
- [ ] тесты: fake-эмбеддер считает пакеты (batch ≥ N при достаточном
      количестве чанков), сохранение порядка chunk-id, индемпотентность с
      `doc_hashes`/`--persist-dir` (повторный прогон skip-ит всё)
- [ ] `go test ./...`, `go vet`, `gofmt` — чисто

### Task 5: Scale-тест ретривала (код при необходимости)

- [ ] проиндексировать ~100K доков (одна крупная часть корпуса)
- [ ] замерить: один запрос через `retriever.Retrieve` с
      `KB_ANN_PREFILTER=true` (FTS5-кандидаты + `QueryCandidates`): p50/p95
      латентность, recall против expected_doc_ids на подмножестве вопросов
- [ ] замерить `KB_ANN_PREFILTER=false` (brute-force) на том же индексе —
      для отчёта и решения, каким путём идёт финальный прогон
- [ ] если латентность > ~10с/запрос или кандидаты бьют мимо: профиль →
      оптимизации (лимит кандидатов, FTS5-ранжирование, параллельный cosine);
      иначе — продолжать на полный корпус
- [ ] удалить тестовый индекс (не копить место)

### Task 6: Полная индексация

- [ ] `KB_INDEX_GRAPH=false` (500K × 17с/док = 99 дней — граф не участвует),
      `KB_HYBRID=true`, `KB_CHUNK_SIZE=4096`, `KB_EMBED_INDEX_BASE_URL=<ai-box>`
- [ ] `kb bench --corpus <corpus> --questions questions.jsonl
      --persist-dir <disk>/erb-persist` (проверено по коду `bench.go`:
      `--limit`/`--types` фильтруют только вопросы, индексация проходит по
      всем докам корпуса — батчевание вопросов не трогает индекс)
- [ ] залогировать фактическое s/док; DB-размер; проверить целостность
      (`kb doctor` / chunk count ≈ 600-650K)

### Task 7: Ответы на 500 вопросов

Конфигурация: `KB_LLM_NO_THINK=true`, `KB_HYBRID=true`, `KB_ANN_PREFILTER=true`,
`KB_RERANK=llm`, `KB_DETECT_CONTRADICTIONS=true`, `KB_QUALIFIER_FILTER=true`,
`KB_SUPERSEDE_MODE=strict`, `KB_SET_MAX_ROUNDS`/`KB_CANDIDATE_K` под
completeness/project_related, concurrency 3.

- [ ] per-type answer mode:
      - `naive` (один Retrieve + один Chat) для basic+semantic (300 вопросов,
        ~5-10с/вопрос ≈ 1ч) — их вопросы однодоковые, GoT там не нужен;
      - GoT (`got.Orchestrator`) для intra_document, project_related,
        constrained, conflicting_info, completeness, miscellaneous,
        high_level (200 вопросов ≈ 2ч);
      - info_not_found: проверить, что abstain-порог срабатывает
        (`KB_ABSTAIN_THRESHOLD`), ответ не выдумывается
- [ ] реализовать per-type режим в `cmd/kb/bench.go` (флаг
      `--answer-mode basic` не подходит — нужно `--naive-types` /
      `--got-types` CSV или конфиг-таблица) — Task 4-adjacent код
- [ ] прогнать, сохранить `answers.jsonl` + `.report.json`
- [ ] если бюджет превышен — резать по типам и мёржить

### Task 8: Локальный прокси-скоринг (код)

Лидерборд недоступен до сабмита — нужен локальный аналог до письма:

- [ ] facts-based оценка качества: покрытие `answer_facts` ответом kb через
      `phraseStemsPresent` (переиспользовать `internal/bench/dragon/score.go`
      логику, вынести в общий пакет или скопировать с ссылкой) — это прокси
      `completeness`
- [ ] per-type отчёт: recall (уже есть), abstain, cited, facts-покрытие,
      invalid-extra-docs (submitted doc_ids, которых нет в expected)
- [ ] `kb bench compare` между конфигами (baseline/candidate), если будет
      второй прогон
- [ ] тесты: покрытие facts на RU/EN золотых парах из testdata/lang-bench

### Task 9: Сабмишен

- [ ] `answers.jsonl` в формате сабмита (все 500 вопросов, `document_ids`
      только ретривнутые — `CorpusDocumentIDs` уже чистит синтетику)
- [ ] `docs/bench/enterpriserag-bench-report.md`: конфигурация, хардвар,
      время, per-type метрики, известные компромиссы (без графа, chunk 4096)
- [ ] reproducibility guide: полная последовательность команд
      (download → unzip → build → index → answers → score) — публичная часть
      сабмишена (kb open source)
- [ ] письмо joachim@onyx.app: ссылка на репо+гайд+`answers.jsonl`
- [ ] обновить README (секция EnterpriseRAG-Bench: ссылка на отчёт)

## Риски

- **Диск** — главный риск; 12GB свободных на Mac уже не хватает (Task 1).
- **RAM**: `LoadCorpus` держит все 512K доков в памяти (~4GB) — на 16GB ок;
  при OOM — стриминг-лоадер (fail-open приём, не блокер).
- **FTS5 на 640K строк** — построение инкрементальное, деградации не ждём;
  проверить в Task 5 на 100K.
- **Контенция с ai-box**: stage5-temporal прогон (100 вопросов) ещё идёт —
  пилоты и финальный прогон ставить после него; иначе и эмбеддинг, и чат
  деградируют и замеры врут.
- **Per-doc HTTP-оверхед при индексации** подтверждён по коду: `indexer.go:689`
  делает один `Embed` на документ — без Task 4 индексация 512K доков ≈ 25-30ч.
- **Per-type answering** — новый код (Task 7); минимальный объём, тесты.

## Post-Completion

- `go test ./...`, `go vet ./...`, `gofmt -l .` — чисто (TDD-правило).
- revmux-pass по новому коду (Task 4/7/8) до доверия числам — прецедент с
  завышенными скорами от self-authored скор-кода (bag-of-stems в dragon).
- Ручная выборочная проверка 5-10 ответов против исходных документов.
