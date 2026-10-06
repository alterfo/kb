# DRAGON: официальный интерфейс оценки и подачи результата

Исследование репозитория `github.com/RussianNLP/DRAGON` и HF Space
`ai-forever/rag-leaderboard` (2026-10-01) — Task 1 плана
`docs/plans/20260930-dragon-leaderboard-submission.md`.

## Главный вывод: официальная оценка — НЕ live-сервис

План изначально предполагал, что официальный `rag-bench` клиент требует
"живых" HTTP-хуков retriever/generator. Это **не так**. Официальная оценка —
чистая офлайн-функция:

```python
from rag_bench import evaluator
evaluation_results = evaluator.evaluate_rag_results(results, qa_dataset, text_mapping)
```

где `results` — обычный `dict`, **в точности совпадающий по форме** с тем,
что kb уже производит (`dragon.SubmissionEntry{FoundIDs, ModelAnswer}` /
`docs/bench/dragon-hist-answers.json`):

```python
results[public_id] = {
    "found_ids": [...],      # retrieved PUBLIC text ids
    "model_answer": "...",   # generated answer text
}
```

Значит, **Python-шим с HTTP-эндпоинтами `/retrieve` и `/generate` не нужен**.
Вместо этого нужен путь: взять уже готовый self-run submission kb → прогнать
через официальный `rag_bench.evaluator` на Python → получить официальные
числа.

## Как устроен официальный eval (из `lib/src/rag_bench/`)

### `baseline.py` (референсная реализация, можно игнорировать)
`init_retriever`/`init_generation`/`get_results` — Chroma + LangChain +
vLLM-референс. Это их собственный baseline, не интерфейс для внешних систем.
Нужен только как пример формата вывода.

### `data.py`
`get_datasets(is_hist=True)` — грузит `HistTexts`/`HistQuestions` с HF,
определяет **текущую версию датасета** (`get_latest_version`). Версии
датасета — git-теги HF-датасета (`1.15.0` = декабрь 2025 и т.д.), **датасет
обновляется ежемесячно**.

### `evaluator.py` — собственно оценка
`evaluate_rag_results(results, dataset, text_mapping)`:
- **Retrieval**: Hit Rate, MRR (поддержка нескольких relevant-документов на
  вопрос, `text_ids` парсится через `ast.literal_eval`, может быть
  вложенным списком — аналог `flattenTextIDs` в `internal/bench/dragon/score.go`).
- **Generation**: ROUGE-1/2/L (через `rouge_score` с кастомным русским
  snowball-токенизатором), Exact Match, Substring Match.
- Агрегация **overall** + **по типу вопроса** (`sample["type"]`).
- Чистит `THINK_END_TOKEN` (`</think>`) из ответа перед метриками.

Это **другой скорер**, чем самописный bag-of-stems kb
(`internal/bench/dragon/score.go`) — официальные числа будут отличаться от
self-score kb (`75.2%` — это kb-метрика `answer_contains_gold`, не ROUGE/EM).

### `text_mapping` — единственная дополнительная зависимость
Нужен маппинг `public_id → private_id`, иначе Hit Rate/MRR считаются в
неправильном пространстве ID (public retrieval ids vs private gold ids).
Строится так (`examples/calculate_baseline_metrics.py`):

```python
private_texts_ds = load_dataset('ai-forever/hist-rag-bench-private-texts', revision=version)
text_mapping = {item['public_id']: item['id'] for item in private_texts_ds['train']}
```

**Проверено 2026-10-01: `ai-forever/hist-rag-bench-private-texts` доступен
без гейта** через тот же HF datasets-server API, который kb уже использует
в `internal/bench/dragon/loader.go` (`FetchTexts`-подобный вызов). Нужен
новый фетч этого датасета — расширение `loader.go`, не новая инфраструктура.

## Путь для kb: получить официальные числа (пересмотр WS2 Task 2-4)

1. **Не нужен Go↔Python HTTP-мост.** Нужен только тонкий Python-скрипт
   (venv с `pip install -e lib/` из DRAGON-репо, зависимости — `datasets`,
   `rouge_score`, `nltk`/snowball — **без** vLLM/torch/langchain, те нужны
   только для `baseline.py`, которым kb не пользуется).
2. Скрипт: прочитать существующий self-run submission kb
   (`docs/bench/dragon-hist-answers.json`, уже в нужном формате
   `{public_id: {found_ids, model_answer}}` — проверить, что ключи те же
   `public_id`, не внутренний `id`), получить `qa_dataset` + `text_mapping`
   (новый фетч в Go **или** прямо в Python через `datasets.load_dataset`),
   вызвать `evaluate_rag_results()`, сохранить отчёт.
3. Это даёт **честные официальные ROUGE/EM/HitRate/MRR числа** без переделки
   retriever/generator kb и без живого сервиса.

Прежний план (Python-шим + HTTP-эндпоинты `/retrieve`/`/generate` +
`kb bench-dragon serve`) можно **упростить до одного offline-скрипта**.

## Подача результата в лидерборд (пересмотр WS2 Task 6)

Лидерборд — это HF Space `ai-forever/rag-leaderboard` (Gradio,
`https://ai-forever-rag-leaderboard.hf.space`), хранящий данные в
собственном `results.json` (версия датасета → submission_id → запись).

Официальный клиентский путь — **не git PR**, а HTTP POST из
`rag_bench.results.submit()`:

```python
response = requests.post(f"http://{address}/submit/create", data={...}, files={path: file})
```

на бэкенд `/submit/create` (адрес бэкенда в открытом коде не захардкожен —
нужно спросить напрямую, см. ниже). Описание в UI лидерборда прямо говорит
про `submit_id`/клиент — это **живой, но, по словам Алёны, более не
поддерживаемый бэкенд**. Отсюда и её совет "пришлите PR" — вероятно, имелся
в виду **PR в сам HF Space-репозиторий** (`huggingface.co/spaces/ai-forever/rag-leaderboard`,
правка `results.json` напрямую — HF Space это обычный git-репозиторий,
PR через HF web UI), а не в `github.com/RussianNLP/DRAGON`, где
результатов/лидерборда вообще нет в дереве репозитория.

**Открытый вопрос, который не закрывается чтением кода**: подтвердить у
Алёны (в том же письме/треде), ожидает ли она PR в GitHub-репозиторий
DRAGON или в HF Space `ai-forever/rag-leaderboard`, и актуальна ли схема
`results.json`, которую описывает `README.md` Space'а (она слегка
расходится с реальным форматом `results.json` — see ниже).

## Схема записи в `results.json` (из реального файла, не из README-примера)

```json
{
  "items": {
    "<dataset_version>": {
      "<submission_id (hex guid)>": {
        "model_name": "...",
        "timestamp": "ISO8601",
        "config": {
          "embedding_model": "...",
          "retriever_type": "mmr",
          "retrieval_config": {"top_k": 20, "chunk_size": 500, "chunk_overlap": 100}
        },
        "metrics": {
          "<question_type>": {
            "retrieval": {"hit_rate": 0.0, "mrr": 0.0, "precision": 0.0},
            "generation": {"rouge1": 0.0, "rougeL": 0.0}
          },
          "overall": { "...": "..." },
          "judge": { "...": "дополнительные LLM-judge метрики, опционально" }
        },
        "metadata": {"n_questions": 600, "submit_timestamp": ""}
      }
    }
  }
}
```

Примечание: README Space'а показывает упрощённую (устаревшую?) схему без
разбивки по `question_type` и без `judge` — реальный файл богаче. Писать
отчёт по реальной схеме, не по README.

## Что остаётся сделать (обновляет Implementation Steps плана)

- Task 2 (Go HTTP-эндпоинты) и Task 3 (Python-адаптер) из текущего плана —
  **упраздняются**, заменяются одним offline Python-скриптом оценки.
- Task 1 расширяется: fetch `hist-rag-bench-private-texts` для
  `text_mapping` (новый код в `internal/bench/dragon/loader.go` или прямо в
  Python-скрипте — проще в Python, раз скрипт и так на Python).
- Task 4 (живой прогон) становится: прогон offline-скрипта оценки на
  существующем self-run submission → официальные числа → сравнение с
  self-score.
- Task 6 (PR) — уточнить у Алёны адрес (GitHub repo vs HF Space), затем
  подготовить запись `results.json` по реальной схеме выше.
