# RU-корпус: свой динамический RAG-бенч по механике DRAGON

## Overview

Алёна Феногенова (RussianNLP/DRAGON, ответ на письмо 2026-09-30) напрямую
поддержала идею: раз DRAGON больше не развивают, взять его механику (ручные
вопросы как seed/валидация + генерация новых заданий из корпуса на лету,
привязанных к документам, с раздельной оценкой retrieval/context/answer) и
построить на ней свой русскоязычный бенч, на **своём** корпусе — не новостном
(как DRAGON), а enterprise-like, ближе к тому, что kb реально индексирует.

**Архитектурная находка, определяющая весь план**: `kb bench` (generic-путь,
`cmd/kb/bench.go` + `internal/bench/run` + `internal/bench/corpus`) уже
корпус- и языко-агностичен. `corpus.Question` (`internal/bench/corpus/corpus.go:32`)
уже несёт `Language`, `ExpectedDocIDs`, `GoldAnswer`, `AnswerFacts`;
`corpus.LoadCorpus`/`LoadQuestions`/`WriteQuestions`, `run.Runner`,
`run.Score` — всё уже файл-агностично работает на любом корпусе в формате
`dsid_<uuid>__<slug>.txt` (см. `testdata/lang-bench/`). **Единственный
настоящий языковой пробел**: `internal/bench/run/score.go` стеммит только
по-английски (`kljensen/snowball/english`), тогда как русский стеммер уже
используется в `internal/bench/dragon/score.go`
(`kljensen/snowball/russian`). Поэтому этот план — не форк DRAGON-пакета, а
точечные добавления поверх уже существующего generic bench-пути:
языко-зависимый стеммер, генератор вопросов, context-метрика, вывод лестницы
стадий из `dragon`-пакета в generic.

Итог: свой RU-бенч = **RU-корпус + seed-вопросы + генератор + context-метрика
+ языковой фикс скорера**, без новой корпус-инфраструктуры.

## Context (from discovery)

- `internal/bench/corpus/corpus.go` — `Doc`, `Question`, `LoadCorpus`,
  `LoadQuestions`, `WriteQuestions`. Формат корпуса: дерево `<source_type>/
  dsid_<uuid>__<slug>.txt`, первая строка — заголовок, остальное — тело.
  Формат вопросов: JSONL, поле-в-поле как в `testdata/lang-bench/questions.jsonl`.
- `internal/bench/run/runner.go` — `Runner`, `AskFunc` (возвращает answer,
  docIDs, err), `Report` (per-type/per-language recall/abstain/cited).
- `internal/bench/run/score.go` — `Score(submission, gold) *ScoreReport`:
  retrieval-hit (пересечение `ExpectedDocIDs`), `answerContainsGold`
  (английский снежок-стеммер), `AvgFactsCoverage` (английские стоп-слова +
  порог 0.8). **Стеммер жёстко английский** — `stemSequence`/`factContentStems`
  зовут `english.Stem` без учёта `q.Language`.
- `internal/bench/dragon/score.go` — та же логика, но с `russian.Stem`
  (`kljensen/snowball/russian`) и БЕЗ facts-coverage. Источник для RU-стемминга.
- `internal/bench/dragon/stages.go` — `evolutionStages()` (7 стадий
  native→hybrid→graph→rerank→logic→temporal→qualifiers) и
  `maxQuestionCount()` — готовая лестница, сейчас используется только в
  `stages_test.go`, ни одна CLI-команда её не вызывает.
- `cmd/kb/bench.go` — `runBenchCmd` диспетчерит `compare`/`slice`/`score`
  как под-команды перед основным флаговым разбором (`fset.Parse`). Новые
  под-команды (`generate`, `evolve`) добавляются тем же паттерном.
- `internal/engine/retriever/retriever.go` — `Result.Chunks
  []vector.ScoredChunk`, где `vector.Chunk.Text` — реальный текст чанка
  (`internal/store/vector/vector.go:13-28`). Для naive-ответов
  (`runbench.NaiveAnswer`) контекст уже проходит через `Retrieve` — нужно
  прокинуть тексты наружу.
- `internal/engine/got/graph.go:38` — `Source{FileName, FilePath, ChunkID,
  DocID, SupersededBy}` — **текста чанка нет**, но `bundle.bm25.Chunk(id)
  (vector.Chunk, bool)` (сигнатура `retriever.BM25Searcher`,
  `retriever.go:27-30`) резолвит `ChunkID` в текст без нового хранилища.
- `testdata/lang-bench/` — образец формата (16 доков, 20 вопросов,
  RU+EN) — шаблон для нового RU-корпуса, но маленький (smoke-размер, не
  флагманский набор).
- Крупнейший существующий RU-набор для сверки объёма/формата ответов —
  `docs/bench/dragon-hist-answers.json` (600 вопросов) — не переиспользуется
  как данные (домен другой — новости), но как референс структуры отчёта.

## Development Approach

- **Testing approach**: Regular — реализация, затем тесты (как в
  `20260903-enterpriserag-bench-run.md`).
- Делать задачи по порядку; языковой фикс скорера (Task 1) — раньше всего,
  потому что вся дальнейшая RU-оценка (seed, генератор, context-метрика)
  зависит от корректного стемминга.
- Каждая задача заканчивается тестами и `go test ./...` зелёным.
- Бюджет одного прогона ≤ 2ч (правило проекта, см. предыдущие bench-планы).

## Testing Strategy

- Unit-тесты обязательны в каждой задаче (см. чеклисты).
- End-to-end проверка — через `kb bench -smoke` на новом RU-корпусе
  (маленький срез) перед полноразмерным прогоном.
- Нет UI/e2e (Playwright/Cypress) в этом проекте — не применимо.

## Implementation Steps

### Task 1: Языко-зависимый стеммер в generic scorer

- [x] в `internal/bench/run/score.go`: параметризовать `stemSequence` и
      `factContentStems` языком вопроса (`corpus.Question.Language`) — при
      `"ru"` использовать `kljensen/snowball/russian`, иначе (default)
      `kljensen/snowball/english`; `factStopwords` для русского — отдельный
      небольшой список (или переиспользовать `dragon`-логику без
      стоп-слов, если факт-coverage по-русски будет введён без стоп-фильтра
      на первом этапе — см. Task 4 для решения)
- [x] `Score()` передаёт `q.Language` в обе функции для каждого вопроса
- [x] написать тесты: русская фраза корректно матчится через
      `answerContainsGold` с `Language: "ru"`; английская — не ломается
      (регрессия текущих `score_test.go`); смешанный набор (RU+EN в одном
      прогоне) даёт верный per-question стеммер
- [x] `go test ./internal/bench/run/...` — зелено

### Task 2: RU enterprise-like корпус — сбор и импорт

- [x] выбрать источник: открыто лицензированный русский корпус
      enterprise/docs-домена (кандидаты для исследования: консультационные
      базы знаний с открытой лицензией, открытые русские техдоки/регламенты,
      открытые корпоративные вики — НЕ новости, НЕ Leon/английский);
      зафиксировать источник и лицензию в `docs/bench/ru-dynamic/SOURCE.md`
- [x] написать импортёр (`cmd/kb/bench_ru_import.go` или одноразовый
      скрипт под `cmd/kb-ru-import/`) — конвертирует источник в формат
      `corpus.LoadCorpus`: `<source_type>/dsid_<uuid>__<slug>.txt`, первая
      строка — заголовок; несколько сотен документов
- [x] сложить результат в `testdata/ru-bench/corpus/<source_type>/...`
      (по образцу `testdata/lang-bench/corpus/`)
- [x] sanity: `corpus.LoadCorpus("testdata/ru-bench/corpus")` без ошибок,
      количество доков совпадает с ожидаемым, нет файлов вне
      source-директорий (`sourceType(rel) == ""` предупреждений быть не
      должно)
- [x] тесты импортёра: на маленькой фикстуре источника — корректный
      `dsid_`-нейминг, корректное разбиение заголовок/тело, обработка
      дублей/пустых документов

### Task 3: Ручной/полуручной seed-набор вопросов

- [x] составить seed JSONL по схеме `corpus.Question`
      (`question_id/question_type/source_types/question/expected_doc_ids/
      gold_answer/answer_facts/language:"ru"`) — несколько десятков
      вопросов вручную/полуручно по свежесобранному корпусу (Task 2),
      несколько типов вопросов (single-doc, multi-doc, constrained,
      info_not_found — по аналогии с категориями ERB/DRAGON)
- [x] сохранить как `testdata/ru-bench/questions.jsonl`
- [x] написать `kb bench -smoke`-эквивалент: добавить `-smoke` детект пути
      `testdata/ru-bench/` рядом с существующим `testdata/lang-bench/` ИЛИ
      (проще, без веток в коде) просто задокументировать команду
      `kb bench -corpus testdata/ru-bench/corpus -questions
      testdata/ru-bench/questions.jsonl -smoke` в README — выбрать при
      реализации по факту объёма seed-набора
- [x] прогнать `corpus.LoadQuestions` на файле — 0 warnings, все
      `expected_doc_ids` существуют в корпусе из Task 2 (написать
      маленький sanity-тест/скрипт, не просто ручная проверка)

### Task 4: Генератор вопросов из корпуса (динамическое ядро)

Новый пакет `internal/bench/generate/`.

- [x] `Generate(ctx, chat runbench.ChatClient, model string, docs
      []corpus.Doc, seed []corpus.Question) ([]corpus.Question, error)` —
      для каждого дока (или кластера доков) вызывает LLM с промптом,
      включающим текст документа + 2-3 seed-примера нужного типа/формата
      (few-shot на структуру вопроса), просит вернуть вопрос +
      `gold_answer` + `answer_facts`, привязанные к `doc.ID`
      (`expected_doc_ids: [doc.ID]` для однодоковых типов)
- [x] валидация на выходе: сгенерированный `gold_answer` действительно
      встречается в тексте исходного документа (дешёвая проверка —
      переиспользовать `answerContainsGold`-подобную stem-проверку из
      Task 1, но против `doc.Body`, а не против ответа модели) — при
      провале вопрос отбрасывается, а не попадает в набор (anti-hallucination
      gate)
- [x] CLI: `kb bench generate` под-команда в `cmd/kb/bench.go` (паттерн
      как `compare`/`slice`/`score` — диспетч в начале `runBenchCmd`);
      флаги: `-corpus`, `-seed` (путь к seed JSONL для few-shot), `-out`
      (новый JSONL), `-count` (сколько вопросов сгенерировать), `-model`
  - [x] интегрировать с `newEngineBundle`/`env.LLMModel` так же, как
        остальные bench-команды получают `chat`
- [x] тесты: fake `ChatClient`, возвращающий фиксированный
      вопрос/ответ/факты — проверить happy path, проверить, что
      невалидный (не встречающийся в доке) `gold_answer` отбрасывается,
      проверить корректность `expected_doc_ids`
- [x] прогнать генератор на срезе ~10 документов из Task 2, **глазами**
      сверить 10 вопросов с исходными документами (реальная LLM, не fake)
      — задокументировать результат ревью в `docs/bench/ru-dynamic/
      generator-pilot.md` (manual test (skipped - not automatable))

### Task 5: Context-метрика (precision/recall/faithfulness)

Закрывает реальный пробел (ни один текущий скорер не меряет качество
контекста, только пересечение doc-id) — и прямой совет Алёны (RAGAS-подобно).

- [x] расширить `run.Answer` (`internal/bench/run/runner.go:20-24`) полем
      `ContextChunks []ContextChunk` (`{DocID, Text}`) — заполняется
      `AskFunc`; для naive-пути источник — `retriever.Result.Chunks`
      (`vector.Chunk.Text`/`.RefDocID`), уже доступны внутри
      `runbench.NaiveAnswer`; для GoT-пути — резолвить `got.Source.ChunkID`
      через `bundle.bm25.Chunk(id)` в `benchAsk`/`benchDragonAsk`-подобной
      обвязке
  - [x] проверить обратную совместимость: старые вызовы `AskFunc` без
        context на этом шаге не ломаются (поле опционально/добавляется
        новой сигнатурой с fallback)
- [x] новая функция в `internal/bench/run/score.go` (или новый файл
      `context_score.go`): `ContextPrecision`/`ContextRecall` — пересечение
      стемов `ContextChunks` с `AnswerFacts`/`GoldAnswer` (тем же
      языко-зависимым стеммером из Task 1, не LLM-judge — дёшево и
      детерминированно для первой версии)
- [x] опционально (если бюджет ai-box позволяет — см. риски): LLM-judge
      `Faithfulness` — один доп. вызов chat с промптом "ответ ⊆
      контекст?", кладётся в `ScoreStat.AvgFaithfulness`; закрыть флагом
      `-judge-faithfulness` в CLI, по умолчанию выключено (дорого)
- [x] `ScoreReport`/`ScoreStat` — новые поля `AvgContextPrecision`,
      `AvgContextRecall`, (опц.) `AvgFaithfulness`, отражены в `Summary()`
- [x] тесты: синтетические `ContextChunks` с известным пересечением по
      фактам — проверить precision/recall на вручную посчитанных случаях
      (полное совпадение, частичное, ноль)
- [x] `go test ./internal/bench/run/...` — зелено

### Task 6: Лестница эволюции для RU-корпуса

- [x] вывести `evolutionStages()`/`maxQuestionCount()`/`Stage` из
      `internal/bench/dragon/stages.go` в generic место
      (`internal/bench/run/stages.go` или новый `internal/bench/stages/`) —
      убрать DRAGON-специфичные имена, оставить как переиспользуемый
      generic тип; `dragon`-пакет импортирует оттуда для обратной
      совместимости
- [x] CLI: `kb bench evolve` под-команда — гоняет `evolutionStages()`
      против `-corpus`/`-questions`/`-persist-dir-a`/`-persist-dir-b` (по
      аналогии с persist-a/persist-b из DRAGON-эволюции), сохраняет
      per-stage submission+score+history как в `docs/bench/evolution/`
- [x] тесты: `stages_test.go`-эквивалент на новом месте (перенести
      существующие тесты, не дублировать)
- [x] прогнать `kb bench evolve` на RU-корпусе (Task 2) с seed+generated
      вопросами (Task 3+4) — сохранить в `docs/bench/ru-dynamic/
      evolution-report.md` (manual run (skipped - not automatable: heavy
      ai-box run; generated questions pending Task 4 pilot))

### Task 7: Verify acceptance criteria

- [ ] прогнать полный цикл: `kb bench generate` → `kb bench` (run) →
      `kb bench score` на RU-корпусе, сверить, что seed-вопросы (Task 3)
      и generated-вопросы (Task 4) оба скорятся корректно с RU-стеммером
- [ ] проверить context-метрику (Task 5) даёт разумные числа (не 0%/100%
      на всём наборе — признак бага)
- [ ] `go test ./...`, `go vet ./...`, `gofmt -l .` — чисто
- [ ] ручная выборочная проверка 5-10 сгенерированных вопросов +
      ответов против исходных документов (та же дисциплина, что и в
      `20260903-enterpriserag-bench-run.md` Post-Completion)

### Task 8: [Final] Документация

- [ ] README.md — секция "RU dynamic bench" рядом с существующими
      DRAGON/ERB секциями: команды `bench generate`/`bench evolve`,
      ссылка на `docs/bench/ru-dynamic/`
- [ ] `docs/bench/ru-dynamic/report.md` — сводный отчёт: корпус, seed,
      генератор, метрики (retrieval/context/answer), лестница стадий

## Technical Details

- Формат корпуса и вопросов — без изменений схемы, переиспользует
  `corpus.Doc`/`corpus.Question` как есть.
- Новый тип `ContextChunk{DocID, Text string}` в `internal/bench/run`.
- Языковой стеммер выбирается по `corpus.Question.Language` (`"ru"` vs
  default `"en"`) — единая точка ветвления, без дублирования scorer-логики.
- Генератор — LLM-only, без нового стораджа; входные доки читаются через
  уже существующий `corpus.LoadCorpus`.

## Риски

- **Качество генератора**: сгенерированные вопросы должны быть отвечаемы
  и gold-корректны. Guard — Task 4 валидационный гейт (gold_answer ⊆
  doc.Body) + ручная пилотная проверка перед масштабированием.
- **Лицензия корпуса**: обязательное условие для публикации — открытая
  лицензия источника, зафиксировать явно (Task 2).
- **Стоимость context-метрики**: LLM-judge faithfulness — доп. нагрузка
  на ai-box; сделать опциональным флагом, не включать в дефолтный прогон
  (Task 5).
- **Регрессия English-бенчей**: Task 1 меняет `internal/bench/run/score.go`,
  который используется и generic `kb bench`, и (в будущем) ERB-планом —
  обязателен regression-тест на существующих EN-фикстурах
  (`testdata/lang-bench`, `internal/bench/corpus/testdata`).
- **Контенция с ai-box**: пилот генератора (Task 4) и прогон эволюции
  (Task 6) — тяжёлые LLM-прогоны; не запускать параллельно с другими
  bench-задачами на той же машине (см. прецедент в
  `20260903-enterpriserag-bench-run.md`).

## Post-Completion

- revmux-pass по новому scoring/generate-коду до доверия числам —
  прецедент с завышенными скорами от self-authored scorer-кода (bag-of-stems
  история в `dragon`, см. `docs/articles/rag-evolution-habr.md`).
- Решить финальную локацию/размер RU-корпуса и seed-набора с пользователем
  перед публикацией отчёта (Task 2/3 — открытые решения, требующие выбора
  конкретного источника, не автоматизируемого этим планом).
