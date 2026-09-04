# kb memory compile: Go-порт компилятора PLE-патчей

## Overview

Порт компилятора `.plepatch`-патчей из проекта `ortegaalfredo/ngram-knowledge-injector`
(Go вместо Python) в `kb`: команда `kb memory compile` превращает curated-знания
(JSON с trigger-фразами и операциями) в бинарный `.plepatch`-файл, который
`llama.cpp-NLTM` (запатченный форк llama.cpp) подхватывает на лету для модели
Qwen3.8-Flash-Next (`qwen4exp`).

**Что это решает.** PLE n-gram таблица модели (51B hash-адресуемых строк) — это
встроенная long-term память. Патч пишет векторы в конкретные строки таблицы:
когда модель видит trigger-фразу, инжектированный сигнал подаётся в residual
stream. Runtime-половина (hot-swap) — форк llama.cpp-NLTM, **не часть kb**;
kb поставляет только компилятор патчей (вторая половина проекта).

**Границы:**
- Только overlay-режим (`.plepatch`-sidecar). Режимы `materialize`/`in-place`
  из Python-инструмента не портируем (нужен GGUF-writer на 54 GB таблицу).
- Операции: `set`, `zero`, `random`, `copy_from`. `blend`/`add`/`scale` — вне
  скоупа (нужны только для ручных экспериментов с векторами).
- Токенизатор: только GGUF-BPE (из метаданных модели), без HF `tokenizer.json`.
- Инспекция (`ple_dump`-аналог) не портируется; манифест патча читаем JSON-ом,
  строки — hexdump.

## Context (from discovery)

- **Files/components involved:**
  - `cmd/kb/main.go:14-71` — диспетчер команд (двухпроходный switch: валидация
    имени + диспатч; `usage()`). Compound-прецедент: `bench compare`
    (`cmd/kb/bench.go:19-22`).
  - `cmd/kb/*.go` — по файлу на команду; `flag.NewFlagSet`, parse-error → exit 2,
    runtime-error → exit 1 (`doctor.go:22-42`, `verify.go:17-29`).
  - `internal/state/atomic.go:8-32` — паттерн атомарной записи (temp+rename)
    для `.plepatch`.
  - `internal/store/sqlite/vectorstore.go:597-614` — паттерн `[]float32` ↔ LE bytes.
  - Новое: `internal/gguf` (минимальный GGUF reader/writer) +
    `internal/plepatch` (hash, Q8_0, BPE, план, overlay) + `cmd/kb/memory.go`.
  - Доки: `README.md` `## Usage` (~line 126), `AGENTS.md` «Карта пакетов».
- **Related patterns:** GGUF/quantization-кода в репо нет; хеш-функции — только
  stdlib FNV (simhash.go) — не подходят, нужен свой mul-xor hash из C++-эталона.
- **Dependencies:** ни одной новой. `encoding/json`, `hash/sha1`, `math/rand/v2`,
  stdlib `regexp` — всё из stdlib.
- **Референс (источник форматов):** `ortegaalfredo/ngram-knowledge-injector`
  (`ple_core.py`, `ple_tok.py`, `inject.py`, `tests/golden/golden.txt`,
  `tests/make_synth.py`) — MIT.

## Development Approach

- **Testing approach**: TDD (обязательно по AGENTS.md; pure algorithms — tests first).
- **CRITICAL: каждый task включает новые/обновлённые тесты** — отдельными пунктами
  чеклиста; все тесты должны проходить до перехода к следующему task.
- Small focused changes; run `go test ./...`, `go vet ./...`, `gofmt -l .`
  после каждого изменения (чистота — критерий готовности).
- Fail-loud на структурных ошибках формата (битый GGUF/патч — ошибка с ясным
  сообщением, никаких паник); компилятор — CLI-инструмент, частичный вывод
  недопустим: файл пишется атомарно только при полном успехе.
- Без комментариев в коде (правило проекта).
- Go only — никакого Python в рантайме и тестах.

## Testing Strategy

- **Unit tests** (без сети/LLM): synthetic GGUF-фикстуры через `gguf.Writer`
  (sparse-подход не нужен в Go — пишем компактную таблицу и настоящие константы;
  аналог `make_synth.py`); golden-векторы хеша из `tests/golden/golden.txt`
  (18 окон, реальные константы модели) — встраиваются как testdata.
- **E2E в CI-смысле**: smoke-тест `cmd/kb/main_test.go` на `run(["memory","compile"...])`
  с synthetic-моделью во временной директории.
- **Live-проверка** против реальной модели — только за
  `//go:build integration` + `KB_LLM_IT=1` (Post-Completion, модель 54+ GB).

## Progress Tracking

- `[x]` — сразу по завершении; ➕ — новые задачи; ⚠️ — блокеры.
- План обновляется при изменении скоупа.

## What Goes Where

- **Implementation Steps** — всё автоматизируемое: код, тесты, доки в репо.
- **Post-Completion** (без чекбоксов) — реальная модель, llama.cpp-NLTM, live-прогон,
  внешние проверки.

## Implementation Steps

### Task 1: `internal/gguf` — Reader/Writer минимального GGUF (v3)
- [x] создать `internal/gguf/reader.go`: парсинг header (magic "GGUF", version=3, tensor_count, metadata_kv_count)
- [x] декодирование всех value types (uint8/int8/uint16/int16/uint32/int32/float32/bool/string/array/uint64/int64/float64) + tensor infos (name, dims u64, type, offset) с alignment
- [x] создать `internal/gguf/writer.go`: минимальный writer (metadata + tensors) для synthetic-фикстур в тестах
- [x] write tests: roundtrip writer→reader на всех value types + строковый массив (tokens)
- [x] write tests: reader на битых/обрезанных файлах возвращает ошибку, не паникует
- [x] run tests — must pass before task 2

### Task 2: `internal/gguf` — shard discovery + locate tensor
- [x] создать `internal/gguf/shards.go`: split.count/split.no из метаданных, поиск шардов по суффиксам (`-00001-of-00006.gguf`, `.00005.gguf`, толерантность к мусорным суффиксам)
- [x] locate tensor by name → data_offset, dims, qtype, n_bytes; ошибка если тензора нет
- [x] write tests: multi-shard fixture, суффиксные паттерны (вкл. download-junk), отсутствующий шард → ошибка
- [x] write tests: locate `per_layer_token_embd.weight` на synthetic fixture; отсутствующий тензор → ошибка
- [x] run tests — must pass before task 3

### Task 3: `internal/plepatch` — hash + row addressing (pure algorithm, golden)
- [x] создать `internal/plepatch/ple.go`: `Constants` (ngram_size, heads_per_ngram, multipliers, offsets, vocab_sizes, eos) из GGUF-метаданных `qwen4exp.ple.*` с валидацией (prefix-sum offsets, длины массивов)
- [x] реализовать `mixed_value` (mul-xor uint64) и `rows_for_token` с EOS-reset-семантикой (eos у t-1 режет t-2; eos у t-2 не трогает ctx[1]; собственный eos токена не режет контекст)
- [x] реализовать `rows_for_sequence` (per-position rows)
- [x] write tests: 18 golden-окон из `tests/golden/golden.txt` (testdata) — byte-exact
- [x] write tests: EOS-reset-семантика (a==b, d!=e кейсы из test_ple.py), rows_in_range property, ошибки валидации метаданных
- [x] run tests — must pass before task 4

### Task 4: `internal/plepatch` — Q8_0 кодек
- [ ] создать `internal/plepatch/q8.go`: quantize/dequantize блока (32 float32 → 34 байт: 32×int8 + scale d fp16; d = amax/127, clamp [-127,127])
- [ ] реализовать `encodeRow`/`decodeRow` (row_dim=160 → 170 байт)
- [ ] write tests: roundtrip byte-identical (как test_q8_roundtrip_byte_identical)
- [ ] write tests: edge-кейсы (нули, отрицательные, amax-границы, неверная длина → ошибка)
- [ ] run tests — must pass before task 5

### Task 5: `internal/plepatch` — GGUF-BPE токенизатор
- [ ] создать `internal/plepatch/bpe.go`: bytes_to_unicode-таблица GPT-2
- [ ] реализовать pre-tokenizer (qwen2- и gpt2-паттерны; `\s+(?!\S)`-альтернатива отбрасывается — RE2 без lookahead, `\s+` покрывает те же матчи) + BPE-merge-цикл по ranks из `tokenizer.ggml.merges` (с кэшем)
- [ ] реализовать загрузку из GGUF-метаданных (`tokenizer.ggml.tokens/merges/pre/byte_fallback/add_prefix_space`)
- [ ] write tests: byte-level vocab без merges (synthetic) — identity для любых строк
- [ ] write tests: hand-built vocab+merges — корректные слияния, qwen2-pattern сегментация, byte_fallback, детерминизм
- [ ] run tests — must pass before task 6

### Task 6: `internal/plepatch` — knowledge schema + план
- [ ] создать `internal/plepatch/knowledge.go`: парсинг entries+defaults (trigger обязателен; ops: set/zero/random/copy_from; at/heads/orders/prefix/note; неизвестный op → ошибка)
- [ ] реализовать build_plan: prefix+trigger → tokens → rows_for_sequence → фильтр heads/orders/positions → target rows → resolve векторов (inline JSON-массив, raw f32 файл, .json файл, random seeded normal*0.02, zero, copy_from: read rows + mean)
- [ ] реализовать last-write-wins по row_ops + подсчёт touched (коллизии)
- [ ] write tests: слияние defaults, невалидные entries → ошибки, фильтры heads/orders, positions (last/all/first/int)
- [ ] write tests: copy_from (mean по heads источника), random детерминизм (same seed → same vector), коллизии считаются
- [ ] run tests — must pass before task 7

### Task 7: `internal/plepatch` — overlay writer + reader
- [ ] создать `internal/plepatch/overlay.go`: 128-байтный header (magic "PLEOVLY1", ver=1, qtype, row_dim, bytes_per_row, n_rows, manifest_len, reserved, 72-байтное имя тензора) + row-записи (u64 row_id + payload)
- [ ] манифест JSON: format/model/table_tensor/table_sha1_before (sha1 затронутых строк с диска)/row_dim/qtype/bytes_per_row/n_rows/ple{...}/entries[{trigger,op,note}]
- [ ] атомарная запись (temp+rename по паттерну internal/state/atomic.go); reader для верификации
- [ ] write tests: roundtrip write→read (по мотивам test_overlay_roundtrip): layout byte-exact, манифест-поля, sha1 по известным байтам строк
- [ ] write tests: атомарность (ошибка до rename → нет файла/нет частичного), bounds-валидация при чтении битого патча
- [ ] run tests — must pass before task 8

### Task 8: `cmd/kb` — команда `kb memory compile`
- [ ] создать `cmd/kb/memory.go`: `runMemoryCmd` — сабкоманда `compile` (иначе usage → exit 2); флаги `-gguf` (обязателен), `-knowledge` (обязателен), `-out` (дефолт: имя knowledge с `.plepatch`), `-report`, `-dry-run`
- [ ] вывод в стиле inject.py: model/table/ple-сводка, plan (unique rows, touched, коллизии), per-entry отчёт; ошибки runtime → exit 1
- [ ] зарегистрировать "memory" в `cmd/kb/main.go` (валидация + диспатч + usage())
- [ ] write tests: smoke `run(["memory","compile"], ...)` на synthetic GGUF в temp-директории (exit 0, magic в файле), `-dry-run` ничего не пишет, `-report` пишет JSON
- [ ] write tests: ошибки использования (нет -gguf/-knowledge, нет сабкоманды) → exit 2; битый GGUF → exit 1
- [ ] run tests — must pass before task 9

### Task 9: Верификация acceptance + документация
- [ ] проверить реализацию всех требований Overview (только overlay, ops set/zero/random/copy_from, GGUF-BPE only)
- [ ] `go test ./...`, `go vet ./...`, `gofmt -l .` — чисто
- [ ] добавить `### memory compile` в `README.md` `## Usage` (пример knowledge.json, вызов, примечание про llama.cpp-NLTM)
- [ ] добавить строки `internal/gguf` и `internal/plepatch` в таблицу пакетов `AGENTS.md`
- [ ] run full test suite — must pass

## Technical Details

### Hash и адресация строк (byte-exact с C++ эталоном `qwen4exp.cpp::set_input`)
```
mixed_n = (ctx[0]*m[0]) ^ (ctx[1]*m[1]) ^ ... ^ (ctx[n-1]*m[n-1])  # uint64
row     = mixed_n % head_vocab_sizes[h] + head_offsets[h]
```
- ctx[0] — текущий токен; 2-grams (n=2) → heads 0..7, 3-grams (n=3) → heads 8..15.
- EOS-reset: при сканировании предшественников от ближнего к дальнему первый
  встреченный eos (или отсутствующий предшественник) заставляет ВСЕ более старые
  слоты стать eos; собственный eos токена не режет его контекст.
- Реальные константы (из метаданных Qwen3.8-Flash-Next Q8_0):
  - multipliers = [23703573157769, 20109073645365, 8052911324071]
  - head_vocab_sizes = [20000003, 20000023, 20000033, 20000047, 20000059,
    20000063, 20000069, 20000077, 20000081, 20000093, 20000107, 20000147,
    20000153, 20000159, 20000161, 20000171]; offsets = prefix sums
  - ngram_size=3, heads_per_ngram=8, n_heads=16, row_dim=160,
    eos_token_id=248044, image_token_id=248056, n_rows=320001446
- Golden: 18 окон `tok p1 p2: <16 rows>` — вызов `rows_for_token(tok, [p2, p1])`.

### GGUF-метаданные, которые читает компилятор
- `general.name`, `general.architecture`, `split.count`, `split.no`
- `qwen4exp.ple.layers`, `.ple.ngram_size`, `.ple.heads_per_ngram`,
  `.ple.conv_kernel`, `.ple.layer_multipliers`, `.ple.head_offsets`,
  `.ple.head_vocab_sizes`, `.ple.eos_token_id`, `.ple.image_token_id`,
  `qwen4exp.embedding_length_per_layer_input`
- `tokenizer.ggml.tokens` (массив строк ~248k), `tokenizer.ggml.merges`,
  `tokenizer.ggml.pre` ("qwen2" → qwen2-pattern, иначе gpt2-pattern),
  `tokenizer.ggml.byte_fallback`, `tokenizer.ggml.add_prefix_space`
- Тензор таблицы: `per_layer_token_embd.weight`, dims [160, n_rows], qtype Q8_0 (id 8).

### Q8_0 (GGML)
- Блок 32 float32 → 34 байта: 32 int8 + scale `d` (fp16).
- `d = max(|x_i|) / 127`; `q_i = round(x_i / d)` c clamp в [-127, 127]; dequant: `x_i = q_i * d`.

### Формат `.plepatch` (фиксированный 128-байтный header)
```
0:8  magic "PLEOVLY1"   8:2 ver=1   10:2 flags=0   12:4 qtype (GGML id)
16:8 row_dim            24:8 bytes_per_row (=170)   32:8 n_rows
40:8 manifest_len       48:8 reserved (0)
56:72 tensor name (NUL-padded, ≤63 байт)
128: manifest JSON; далее записи: u64 row_id + bytes_per_row payload (в порядке возрастания row_id)
```
Манифест: `format="ple-overlay-v1"`, `model`, `table_tensor`,
`table_sha1_before` (sha1 байтов затронутых строк до патча), `row_dim`, `qtype`,
`bytes_per_row`, `n_rows`, `ple{ngram_size, heads_per_ngram, eos_token_id}`,
`entries[{trigger, op, note}]`.

### Knowledge JSON (schema как в инжекторе, урезанный)
```json
{
  "defaults": { "at": "last", "heads": "all", "orders": "all", "note": "" },
  "entries": [
    { "trigger": "The capital of France is",
      "op": "copy_from", "copy_from": "Paris" },
    { "trigger": "Boiling point of water is",
      "op": "set", "vector": [ /* 160 floats */ ] },
    { "trigger": "mitochondria is the",
      "op": "random", "seed": 1 },
    { "trigger": "obsolete fact", "op": "zero" }
  ]
}
```
- `at`: `last`|`all`|`first`|int; `heads`/`orders`: `all` или списки; `prefix` —
  контекст, добавляемый перед trigger-ом (влияет на хеширование).
- `vector`: inline JSON-массив 160 float32, путь к raw f32 / .json файлу,
  `"random"` (seed, normal*0.02), `"zero"`.
- `copy_from`: encode источника → rows_for_sequence → строки последней позиции
  источника для того же набора heads → mean → set в target-строки.
- Коллизии (строка задета несколькими entries): не клобберим — в отчёте,
  last-write-wins в row_ops (как в инжекторе).

### Заметки по реализации
- Go-RE2 не имеет negative lookahead: в pre-tokenizer-паттернах альтернатива
  `\s+(?!\S)` отбрасывается, `\s+` даёт идентичные матчи (проверено семантикой
  альтернаций).
- RNG (`random`): math/rand/v2, детерминирован по seed; точное совпадение с
  numpy-генератором не требуется (вектор произволен, важно лишь постоянство
  внутри kb).
- `gguf.Writer` в продакшне не используется (только overlay-режим); живёт в
  internal/gguf для synthetic-фикстур тестов.

## Post-Completion

**Ручная верификация (вне репо, требует внешних ресурсов):**
- Скачать Qwen3.8-Flash-Next Q8_0 GGUF (6 шардов, ~54 GB таблица + веса; нужно
  достаточно RAM для mmap).
- Собрать форк `ortegaalfredo/llama.cpp-NLTM`, поднять `llama serve`, положить
  `<model>.gguf.plepatch` рядом — проверить hot-swap (редактирование патча без
  перезапуска).
- Live-тест за `//go:build integration` + `KB_LLM_IT=1`: compile → serve →
  запрос с trigger-фразой → семантический эффект; сверить наш `.plepatch` с
  выводом Python-инжектора на реальной модели (parity).

**Внешние системы:**
- Ollama hot-swap НЕ поддерживает (их llama.cpp-форк без overlay-патча) —
  использовать llama.cpp-NLTM; `kb` подключается через `KB_LLM_BASE_URL`.
- Связка с `kb serve` (авто-регенерация патча при изменении notes/approved) —
  отдельный будущий план, не входит в этот.
