# RU dynamic bench: сводный отчёт

Статус: методология и offline-верифицированный seed-скоринг. Живые LLM-прогоны
(generator pilot, run, evolve) — pending; их числа заполняются после доступного
ai-box-прогона (см. Post-Completion в плане).

> Это собственный русскоязычный бенч на enterprise-like корпусе, построенный по
> механике DRAGON: ручные seed-вопросы как валидация + генерация вопросов из
> корпуса на лету, с раздельной оценкой retrieval / context / answer. Абсолютные
> числа эволюционной лестницы сравнимы только как дельты между соседними
> ступенями.

## Корпус

- Источник: doka.guide — открытая русскоязычная документация по веб-разработке.
- Выборка: 298 статей из разделов `tools`, `recipes`, `a11y`.
- Расположение: `testdata/ru-bench/corpus/doka/`, формат
  `dsid_<uuid>__<slug>.txt` (первая строка — заголовок, остальное — тело).
- Лицензия текстов: CC BY-NC-SA 4.0. Для внутреннего некоммерческого бенча
  допустимо; перед публикацией требуется согласование с Докой или переход на
  источник без NC. Подробности и команда воспроизведения импорта:
  `docs/bench/ru-dynamic/SOURCE.md`.

## Seed

- Расположение: `testdata/ru-bench/questions.jsonl`.
- Объём: 39 русскоязычных вопросов (`language: "ru"`), 4 типа:
  - `single-doc` — 23
  - `constrained` — 6
  - `multi-doc` — 5
  - `info_not_found` — 5 (без expected-документов)
- Схема: поля `corpus.Question` (`question_id`, `question_type`,
  `source_types`, `question`, `expected_doc_ids`, `gold_answer`,
  `answer_facts`, `language`).
- Проверено: `corpus.LoadQuestions` без warnings; все `expected_doc_ids`
  существуют в корпусе.

## Генератор

- Пакет: `internal/bench/generate`; CLI: `kb bench generate`.
- На каждый документ генерируется ровно один `single-doc` вопрос через LLM,
  с `expected_doc_ids: [doc.ID]` и `language: "ru"`; до 3 seed-вопросов
  используются как few-shot на структуру ответа.
- Anti-hallucination gate: `gold_answer` обязан совпасть с телом документа по
  тому же языко-зависимому стем-анализу, что использует скорер
  (`run.GoldAnswerInText` → контигуальная последовательность стемов, не
  посимвольное совпадение — допускает регистр, пунктуацию и словоформы);
  иначе вопрос отбрасывается. `answer_facts` проходят отдельную проверку тем
  же bag-of-stems порогом (`run.FactCoveredInText`), так что факт без
  содержательного пересечения с документом отбрасывается. **Известное
  ограничение**: эта проверка (в отличие от скоринга ответов,
  `run.factCovered`) не детектирует отрицание — после нескольких заходов на
  эвристику локального отрицания (глобальная чётность, окно токенов,
  уровень предложения), каждая из которых ломалась на реальных примерах
  (десятичные дроби, разнополярные клаузы в одном предложении), решение —
  не пытаться автоматически детектировать полярность факта против целого
  документа. Остаточный риск (сгенерированный факт — точная инверсия того,
  что говорит документ) покрывается обязательным ручным пилот-ревью
  (ниже), а не эвристикой.
- Флаги: `-corpus`, `-seed` (few-shot JSONL), `-out`, `-count` (сколько
  документов обработать, 0 = все), `-model`.

## Метрики

Скоринг в `internal/bench/run` (`Score`/`ScoreWithJudge`) языко-зависим:
стеммер выбирается по `question.language` (`ru` → `kljensen/snowball/russian`,
иначе `kljensen/snowball/english`).

- Retrieval: `retrieval_hit` — пересечение `document_ids` ответа с
  `expected_doc_ids`.
- Answer: `answer_contains_gold` (стем-подстрока `gold_answer` в ответе),
  `avg_facts_coverage` (доля стемов `answer_facts`, покрытых ответом, порог
  0.8, с языко-зависимыми стоп-словами).
- Context: `avg_context_precision` / `avg_context_recall` — пересечение стемов
  `ContextChunks` (реальный текст чанков из pipeline) с `answer_facts` или
  `gold_answer`.
- Опционально: `avg_faithfulness` — LLM-judge «ответ ⊆ контекст?», включается
  флагом `-judge-faithfulness` (по умолчанию выключен, дорого).

Offline-верификация (Task 7): seed — 39/39 `answer_contains`, retrieval
34/39 (5 `info_not_found` без expected-документов корректно не считаются);
generated-style — 2/2. Контекст-метрика на синтетике даёт разумные значения
(ctx_p 0.74–0.86, ctx_r 1.00 на полном покрытии; unit-тесты покрывают 0 /
partial / full).

## Лестница стадий

`internal/bench/run.EvolutionStages()` — 7 накопительных ступеней; каждая =
предыдущая + ровно одна возможность. `persist-a` индексируется без графа
(ступени 0–1), `persist-b` — с графом (ступени 2–6).

| # | Стадия | Добавляет | Path | Индекс |
|---|--------|-----------|------|--------|
| 0 | native | dense-only retrieval, один LLM-вызов | naive | persist-a |
| 1 | hybrid | hybrid retrieval (dense + BM25) | naive | persist-a |
| 2 | graph | graph extraction + fusion | naive | persist-b |
| 3 | rerank | LLM-rerank | naive | persist-b |
| 4 | logic | Graph-of-Thoughts | got | persist-b |
| 5 | temporal | supersede strict + contradiction detection | got | persist-b |
| 6 | qualifiers | qualifier filter | got | persist-b |

CLI: `kb bench evolve` гоняет все ступени, сохраняя per-stage submission,
score report и history в `--out-dir`. Флаги: `-corpus`, `-questions`,
`-persist-dir-a`, `-persist-dir-b`, `-out-dir`, `-concurrency`, `-top-k`,
`-limit`, `-types`.

## Команды

```sh
# генерация
./bin/kb bench generate \
  --corpus testdata/ru-bench/corpus \
  --seed testdata/ru-bench/questions.jsonl \
  --out generated-ru-questions.jsonl

# sanity-run на checked-in RU-подмножестве
./bin/kb bench --ru-smoke

# полный прогон + скоринг
./bin/kb bench \
  --corpus testdata/ru-bench/corpus \
  --questions testdata/ru-bench/questions.jsonl \
  --out ru-answers.jsonl
./bin/kb bench score \
  --questions testdata/ru-bench/questions.jsonl \
  ru-answers.jsonl

# лестница стадий
./bin/kb bench evolve \
  --corpus testdata/ru-bench/corpus \
  --questions testdata/ru-bench/questions.jsonl \
  --out-dir docs/bench/ru-dynamic/evolution
```

## Статус проверок

- `go test ./...`, `go vet ./...`, `gofmt -l .` — чисто (Task 7).
- Живые прогоны `bench generate` / `bench` / `bench evolve` — pending: ai-box
  chat недоступен в момент Task 7, а пилот генератора (Task 4) помечен manual
  (skipped - not automatable). Числа заполняются после доступного прогона.
