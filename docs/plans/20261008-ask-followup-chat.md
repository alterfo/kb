# Ask follow-up chat with safe web search

## Overview
- После финального ответа на `/ask` пользователь может продолжать диалог: задавать уточняющие вопросы, ответы приходят markdown-ом, рассуждения модели скрыты под спойлеры (как в `askview.go`).
- Контекст уточнения: исходный вопрос, финальный ответ, его источники (`ThoughtGraph.Sources`/`ChunkSources`) и предыдущие реплики треда; при нехватке контекста — повторный ретрив по корпусу.
- Опциональный интернет-поиск (SearXNG на ai-box) строго по явному разрешению пользователя на каждый запрос, с трёхуровневой защитой исходящего запроса: LLM-обобщение → детерминированный фильтр → показ точного запроса и подтверждение. Fail-closed.
- Результаты веба — недоверенные данные: изолируются в промпте, цитируются как внешние источники, не смешиваются с корпусом и не индексируются.
- Тред сохраняется в истории и восстанавливается после рестарта.

## Context (from discovery)
- `internal/web/ask.go` — `askManager`, SSE, `persistAskRun`, `handleAskPage`; `internal/web/askview.go` — рендер markdown и `<think>`-спойлеры.
- `internal/web/server.go` `Deps` — `Chat ChatClient`, `History history.Store`, `Graph`, retriever-настройки.
- `internal/store/history` — интерфейс `Store`, реализация `sqlite.HistoryStore` (`ask_runs`, `search_history`).
- `internal/engine/got`, `internal/engine/retriever` — ретрив и синтез; `internal/engine/report` — `Synthesize`.
- `internal/connectors/searchapi` — коннектор индексации, живого веб-поиска в проекте нет.
- Инфраструктура: SearXNG в docker на ai-box (`192.168.88.193`), `KB_NO_PROXY` для обхода Privoxy.
- Правила проекта: fail-open везде, кроме явных исключений (здесь для web-поиска — fail-closed как осознанное исключение); TDD; без сети и LLM в unit-тестах (fake `ChatClient`, `httptest.Server`); `go test ./...`, `go vet ./...`, `gofmt -l .` чистые; без комментариев в коде.

## Development Approach
- **Testing approach**: TDD (tests first) — правило проекта
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility (существующие `/ask`, history, SSE не ломаются)

## Testing Strategy
- **Unit tests**: на каждый таск; фильтр утечек — table-driven с позитивными и негативными кейсами.
- Веб-поиск: `httptest.Server` под SearXNG, fake `ChatClient`; проверка, что без подтверждения HTTP-запрос не выполняется (счётчик обращений = 0).
- Handler-тесты `internal/web` на весь поток: ask → follow-up → proposal → confirm → ответ.
- Live-LLM только за `//go:build integration` + `KB_LLM_IT=1`.

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## What Goes Where
- **Implementation Steps** (`[ ]`): код, тесты, документация в репозитории.
- **Post-Completion**: ручная проверка в браузере, настройка SearXNG на ai-box, security review.

## Implementation Steps

### Task 1: Outbound query guard (детерминированный фильтр)
- [x] создать пакет `internal/websearch/guard` с `Guard.Check(query) (Verdict, error)`; Verdict содержит причины блокировки
- [x] regex-детекторы: email, телефон, IPv4/IPv6, URL с токеном/userinfo, JWT, AWS/GitHub/Slack-подобные ключи, длинные hex/base64 строки, пути файловой системы, приватные хосты и `*.local`
- [x] словарь запретных терминов: значения имён секретных env из `sources.yaml`, имена источников, пометка `visibility: confidential`, пользовательский `KB_WEBSEARCH_DENYLIST`
- [x] блокировка совпадений с именами сущностей графа и названиями документов корпуса (через узкий интерфейс `TermSource`); ограничение длины запроса
- [x] write tests: table-driven позитивные срабатывания по каждому детектору
- [x] write tests: негативные кейсы (безобидные технические запросы проходят), пустой/слишком длинный запрос
- [x] run tests - must pass before next task

### Task 2: Query generalizer (LLM-обобщение)
- [x] `Generalizer.Propose(ctx, chat, question, answerContext) (string, error)`: промпт требует общий формулировку без имён, идентификаторов, внутренних терминов; контекст корпуса в промпт отдаётся минимально
- [x] вывод модели чистится (`<think>` вырезается), проходит `Guard.Check`; при блокировке — одна повторная попытка с перечислением запрещённого, затем отказ
- [x] write tests с fake `ChatClient`: нормальный ответ, ответ с утечкой → retry → отказ, пустой ответ, ошибка LLM
- [x] run tests - must pass before next task

### Task 3: SearXNG client
- [x] `internal/websearch/searxng`: `Search(ctx, query, n) ([]Result, error)` с JSON API, таймаутом, лимитом размера ответа, обходом прокси по `KB_NO_PROXY`
- [x] конфиг: `KB_WEBSEARCH_URL` (пусто = фича выключена), `KB_WEBSEARCH_MAX_RESULTS`, лимит запросов на тред
- [x] нормализация результатов: title/url/snippet обрезаются, HTML вырезается, дубли по домену ограничиваются
- [x] write tests на `httptest.Server`: успех, не-200, таймаут, огромный ответ, невалидный JSON
- [x] run tests - must pass before next task

### Task 4: Follow-up storage
- [x] расширить `history.Store`: `AppendAskMessage/AskThread` (таблица `ask_messages`: run_id, seq, role, content, sources JSON, web_used, created_at) и миграция в `store/sqlite`
- [x] write tests для sqlite-реализации: append, порядок, восстановление после повторного открытия базы
- [x] write tests для fake-реализации в `internal/web/fakes_test.go`
- [x] run tests - must pass before next task

### Task 5: Follow-up answer engine
- [x] `internal/web/followup.go`: сборка промпта (вопрос, финальный ответ, источники, последние N реплик), опциональный ретрив по корпусу для уточнения
- [x] блок веб-результатов оборачивается в явный разделитель «недоверенные внешние данные», инструкции внутри игнорируются; ссылки на веб-источники помечаются `[web:N]` отдельно от корпусных
- [x] ответ рендерится через `renderAnswer` (markdown + `<think>`-спойлер)
- [x] write tests: промпт содержит контекст и разделитель, веб-блок экранирован, ответ с `<think>` разбирается
- [x] write tests: prompt-injection в сниппете не попадает за разделитель
- [x] run tests - must pass before next task

### Task 6: Web endpoints и поток подтверждения
- [ ] `POST /ask/followup` (текст вопроса, run id), `POST /ask/followup/websearch/propose` (возвращает сгенерированный и прошедший фильтр запрос), `POST /ask/followup/websearch/confirm` (принимает точный запрос от пользователя)
- [ ] confirm повторно прогоняет `Guard.Check` на стороне сервера по тексту, который реально уйдёт; клиентскому тексту не доверяем; одноразовый токен подтверждения привязан к run id и хэшу запроса
- [ ] без валидного токена или при блокировке фильтра HTTP-запрос к SearXNG не выполняется; аудит-лог отправленных запросов (без результатов)
- [ ] соблюдение loopback-политики (`accessguard`) и CSRF-подобной защиты существующих POST
- [ ] write tests: поток propose → confirm → ответ; confirm без токена; изменённый запрос; запрос с секретом блокируется; фича выключена
- [ ] run tests - must pass before next task

### Task 7: UI на странице Ask
- [ ] в `templates/ask.html` после финального ответа: тред сообщений (markdown, спойлеры), форма вопроса, переключатель «искать в интернете»
- [ ] модальный шаг подтверждения: показывает точный исходящий запрос, редактируемый; кнопки «Отправить» / «Отмена», явное предупреждение, что запрос уйдёт наружу
- [ ] веб-источники в ответе отличаются бейджем «внешний, непроверенный»; тред восстанавливается из истории после рестарта
- [ ] стили в `static/kb.css`, без CDN
- [ ] write tests: рендер страницы с тредом и формой, отсутствие переключателя при выключенной фиче
- [ ] run tests - must pass before next task

### Task N-1: Verify acceptance criteria
- [ ] ни один веб-запрос не уходит без явного подтверждения; фильтр fail-closed
- [ ] markdown и спойлеры работают в треде и после перезагрузки страницы
- [ ] `go test ./...`, `go vet ./...`, `gofmt -l .` чистые

### Task N: [Final] Update documentation
- [ ] обновить `AGENTS.md` (карта пакетов: `internal/websearch`, исключение fail-closed), `README.md` (env-переменные), `.env.example`, `CHANGELOG.md`

## Technical Details
- Env: `KB_WEBSEARCH_URL`, `KB_WEBSEARCH_MAX_RESULTS` (дефолт 5), `KB_WEBSEARCH_DENYLIST`, `KB_WEBSEARCH_MAX_PER_THREAD`.
- Поток: вопрос → (ответ по корпусу) или (propose → фильтр → показ → confirm → поиск → ответ с `[web:N]`).
- Веб-контент никогда не индексируется и не пишется в корпус без отдельного действия пользователя.
- Явное исключение из fail-open: любая ошибка guard/generalizer/подтверждения = поиск не выполняется, ответ даётся только по корпусу с уведомлением.

## Post-Completion
**Manual verification**:
- Пройти в браузере поток с запросом, содержащим email/ключ/внутренний термин — убедиться в блокировке.
- Проверить поведение при недоступном SearXNG.

**External system updates**:
- Включить JSON-формат в настройках SearXNG на ai-box и открыть доступ только с Mac.
- Security review фильтра и потока подтверждения.
