# DRAGON: официальная оценка и подача результата в лидерборд

## Overview

Алёна Феногенова (RussianNLP/DRAGON, ответ 2026-09-30) лично пригласила
прислать результат kb, раз лидерборд больше не поддерживают
централизованно, но проект открытый. Текущий `kb bench-dragon`
(`cmd/kb/dragon.go`) даёт только **self-scored** прогон: 75.2%
answer_contains на hist-корпусе (542 текста/600 вопросов), честно
помеченный в коде и README как "self-run submission file... not an official
DRAGON leaderboard score" (`cmd/kb/dragon.go:170`).

**План пересмотрен после исследования (Task 1, см.
`docs/bench/dragon-leaderboard/official-interface.md`).** Изначальное
предположение — что официальная оценка требует live Python-клиент с
HTTP-хуками retriever/generator — оказалось неверным. Официальная оценка
(`rag_bench.evaluator.evaluate_rag_results`) — **чистая офлайн-функция**,
принимающая `dict` в точности той формы, которую kb уже производит
(`{public_id: {found_ids, model_answer}}`). Поэтому вместо Go↔Python
HTTP-моста нужен один offline Python-скрипт оценки поверх уже готового
self-run submission kb. Подача в лидерборд (HF Space
`ai-forever/rag-leaderboard`) тоже устроена иначе, чем предполагалось —
см. детали в Task 2 ниже.

## Context (from discovery)

- `github.com/RussianNLP/DRAGON`, пакет `lib/src/rag_bench/`:
  `evaluator.py` — `evaluate_rag_results(results, dataset, text_mapping)`:
  Hit Rate/MRR (retrieval) + ROUGE-1/2/L/EM/substring-match (generation,
  русский snowball-токенизатор), агрегация overall + по типу вопроса.
  `baseline.py` (Chroma+LangChain+vLLM референс-ретривер/генератор) —
  **не нужен**, это их пример, не интерфейс для внешних систем.
- `text_mapping` (public→private id) строится из
  `ai-forever/hist-rag-bench-private-texts` — **проверено 2026-10-01,
  доступен без гейта** через тот же HF datasets-server API, которым уже
  пользуется `internal/bench/dragon/loader.go`.
- Лидерборд — HF Space `ai-forever/rag-leaderboard` (Gradio), хранит записи
  в собственном `results.json` (версия датасета → submission_id → запись
  с `model_name`/`config`/`metrics` по типам вопросов + overall).
  Официальный клиентский путь подачи — HTTP POST
  (`rag_bench.results.submit()` → `/submit/create`) на бэкенд, адрес
  которого не захардкожен в открытом коде и который, по словам Алёны,
  "не поддерживается в актуальном состоянии" (скорее всего, не работает).
- `docs/bench/dragon-report.md` — self-scored 75.2%/0 hallucinated citations
  на `Hist*` датасетах — держать как референс для сверки с официальным
  числом (Task 3).
- Этот план идёт параллельно `20260930-ru-dynamic-bench.md`, не блокируя
  друг друга (независимый код), но конкурирует за ai-box при живых
  прогонах — координировать расписание.

## Development Approach

- **Testing approach**: Regular — реализация, затем тесты.
- Task 1 — чисто исследовательская (формат клиента), без кода; остальные
  задачи следуют обычной дисциплине "код → тесты → зелёные тесты → следующая
  задача".
- Бюджет одного прогона ≤ 2ч (правило проекта).

## Testing Strategy

- Unit-тесты (`pytest`) обязательны для offline-скрипта оценки (Task 2) —
  на фикстурных данных, без реальных HF-датасетов в тестах.
- Нет UI/e2e в этом проекте — не применимо.
- End-to-end проверка — прогон на уже существующем self-run submission
  (Task 3), сверка с self-score перед подачей (Task 5).

## Implementation Steps

### Task 1: Разобрать официальный интерфейс приёма — ВЫПОЛНЕНО

- [x] изучить `github.com/RussianNLP/DRAGON` — найден `lib/src/rag_bench/`:
      `evaluator.evaluate_rag_results(results, dataset, text_mapping)`,
      офлайн, без retriever/generator hooks
- [x] найти инструкции по формату подачи — HF Space `results.json` +
      HTTP `/submit/create` (вероятно неактуален); PR, скорее всего, в
      сам HF Space, не в GitHub-репозиторий DRAGON (нужно подтвердить)
- [x] зафиксировать находки в
      `docs/bench/dragon-leaderboard/official-interface.md`
- [x] решено: НЕ нужен HTTP-шим/сервис — офлайн Python-скрипт поверх
      готового self-run submission kb

### Task 2: Offline-скрипт официальной оценки

Новый `tools/dragon-eval/` (вне Go-модуля, не Go-пакет — чистый Python,
т.к. зависит от `rag_bench`/`rouge_score`/`datasets`).

- [ ] venv + `pip install -e <DRAGON-repo>/lib` (клонировать DRAGON-репо
      в `tools/dragon-eval/vendor/` или рядом, зафиксировать pinned-коммит
      как в `docs/bench/ru-dynamic/SOURCE.md`-примере)
- [ ] `evaluate.py`:
      1. прочитать self-run submission kb (`docs/bench/dragon-hist-answers.json`),
         проверить/привести ключи к `public_id` (int), как ожидает
         `evaluate_rag_results`
      2. `data.get_datasets(is_hist=True)` → `qa_dataset`, версия датасета
      3. построить `text_mapping` из `ai-forever/hist-rag-bench-private-texts`
         (`{item['public_id']: item['id']}`, см. `official-interface.md`)
      4. `evaluator.evaluate_rag_results(results, qa_dataset, text_mapping)`
      5. сохранить `average_metrics` (overall + по типу) в JSON-отчёт
- [ ] расхождение в ключах: уточнить при реализации, использует ли
      `docs/bench/dragon-hist-answers.json` `public_id` или внутренний
      `id` как ключ (`dragon.Score` в Go использует `g.PublicID` — скорее
      всего совпадает, но проверить фактическим прогоном)
- [ ] тесты: `pytest` с маленьким фикстурным `results`/`qa_dataset`/
      `text_mapping` (3-5 вопросов) — проверить, что скрипт не падает и
      агрегирует метрики корректно; реальные HF-датасеты в тестах не грузить
- [ ] `requirements.txt` с pinned-версиями (`datasets`, `rouge_score`,
      зависимости `rag_bench` — без vLLM/torch/langchain, они не нужны
      для `evaluator.py`)

### Task 3: Прогон и сверка с self-score

- [ ] прогнать `evaluate.py` на уже существующем self-run submission
      (`docs/bench/dragon-hist-answers.json`, 600 вопросов) — официальные
      ROUGE-1/2/L, EM, substring_match, Hit Rate, MRR
- [ ] зафиксировать в `docs/bench/dragon-leaderboard/official-run-report.md`:
      официальные числа, конфигурация (модель/эмбеддер из
      `docs/bench/dragon-report.md`), сравнение с self-score kb
      (75.2% answer_contains — другая метрика, не ждать точного совпадения,
      объяснить разницу методологий)
- [ ] если что-то в маппинге/версии датасета не бьётся (например, версия
      self-run submission старше текущей версии датасета) — перегенерировать
      self-run submission свежим `kb bench-dragon` прогоном на актуальной
      hist-версии, а не подгонять скрипт под устаревшие данные

### Task 4: Verify acceptance criteria

- [ ] `go test ./...`, `go vet ./...`, `gofmt -l .` — чисто (Go-часть
      проекта не меняется этим планом, но прогнать для общей гигиены)
- [ ] `pytest tools/dragon-eval/` — чисто
- [ ] официальный прогон воспроизводим "с нуля" по записанным в Task 2-3
      командам (чистый venv, свежий фетч датасетов)

### Task 5: [Final] Подать результат + документация

- [x] адрес подачи подтверждён — PR в `github.com/RussianNLP/DRAGON`
      (не HF Space `results.json`)
- [ ] подготовить запись по реальной схеме, ожидаемой PR в DRAGON-репо
      (уточнить точный формат/путь внутри репозитория при реализации —
      `official-interface.md` описывал схему HF Space `results.json`,
      нужно перепроверить, совпадает ли она с тем, что ожидает сам
      GitHub-репо, или там свой формат)
- [ ] README.md — обновить секцию DRAGON: убрать формулировку "не
      официальная отправка", добавить ссылку на официальный отчёт/PR
      (после подтверждения, что принято, либо пометить "отправлено,
      ожидает ревью")
- [ ] отправить PR в `github.com/RussianNLP/DRAGON` с числами из Task 3

## Technical Details

- Оценка идёт на **hist**-датасетах (`Hist*`), на которых уже сделан
  self-run submission (`docs/bench/dragon-hist-answers.json`) — не нужна
  повторная индексация/прогон kb, если версия датасета совпадает
  (см. риск "Версия датасета" выше).
- `evaluate_rag_results` ожидает `results` ключами `public_id` (int);
  `text_mapping` строится из `ai-forever/hist-rag-bench-private-texts`
  (не из `hist-rag-bench-private-qa`, который kb уже использует для
  self-score — это два разных приватных датасета).

## Риски

- ~~**Адрес подачи не подтверждён**~~ — снято: Алёна подтвердила, подача —
  PR в `github.com/RussianNLP/DRAGON`. Остаётся уточнить точный формат
  записи/путь внутри репозитория при реализации Task 5.
- **Лидерборд мог полностью не поддерживаться технически** (сама Алёна
  пишет "не поддерживаем в актуальном состоянии") — `/submit/create`,
  возможно, не работает; PR/правка `results.json` может быть принята не
  автоматически — закладывать время на ручную коммуникацию.
- **Расхождение self-score vs official-score** — Task 3 явно требует
  сверки и объяснения разницы методологий (bag-of-stems kb vs
  ROUGE/EM/substring официального скорера), не просто "чем больше число,
  тем лучше".
- **Версия датасета** — `hist-rag-bench-*` обновляется ежемесячно;
  self-run submission (`docs/bench/dragon-hist-answers.json`) мог быть
  сделан на более старой версии, чем текущая `text_mapping`/`qa_dataset` —
  Task 3 должен проверить совпадение версий, а не молча считать на
  рассинхронизированных данных.
- **Контенция с WS1** (`20260930-ru-dynamic-bench.md`) за ai-box — этот
  план теперь почти не требует ai-box (офлайн-скрипт, не живой прогон),
  но при необходимости перегенерировать self-run submission (Task 3) —
  координировать расписание.

## Post-Completion

- Дождаться реакции мейнтейнеров DRAGON на PR — внешний, не
  автоматизируемый шаг.
- После публикации/смёржа PR — обновить
  `docs/articles/rag-evolution-habr.md` ссылкой на официальный результат
  (эта статья сама по себе вне скоупа этого плана, но выигрывает от его
  результата).
