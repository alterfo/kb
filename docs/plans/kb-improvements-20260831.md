# План улучшений kb — 2026-08-31

Сводный план по итогам трёх консультаций:
- deepseek-v4-pro (эта сессия) — первичный список;
- qwen3.8:latest — критика и дополнения (транскрипт: `model-duel-20260831-kb-improvements.md`);
- Claude Code (sonnet) — независимый архитектурный обзор (полный текст: `/tmp/claude-code-plan.md`).

Итог — консенсус с расхождениями, отмеченными явно.

Примечание: подсистема `kb plan` (internal/planner) удалена из проекта как ошибочно созданная —
пункт Claude Code про RCE в её bash-туле закрыт удалением, а не харднением.

## P0 — корректность (до следующего релиза)

1. **GoT-баги из BACKLOG (S).** `cleanSubgoalItems` не ремапит `depends_on` (decompose.go:201); пустая "Previously resolved sub-answers" для cycle-broken/self-deps (orchestrator.go:462); rolling memory не капает dependent-инъекции. Плюс property-тесты: no dangling subgoals, стабильный ремаппинг, deterministic fake-LLM replay (qwen + A).
2. **Backup/DR для kb.db (S) — только Claude Code.** `kb backup` поверх `VACUUM INTO`, `PRAGMA integrity_check` в `doctor`, абзац в README. Весь корпус/граф/история — один файл без recovery-пути.
3. **Битая ссылка на `docs/architecture-review.md` (S) — только Claude Code.** Файл не существует (удалён); ссылки остались в `AGENTS.md:154` и `README.md:73` — восстановить файл из git или убрать обе ссылки.

## P1 — измеримость и масштаб retrieval

4. **Качество-гейт в CI (M).** Детерминированный regression-сьют на `testkit` fake-LLM: golden-запросы + пороги recall/precision + DAG-инварианты, внутри `go test ./...` (все трое сошлись). Фокус на инвариантах, не на overfitted-строках (Claude).
5. **Метрики + видимость деградации (M).** recall@k/latency/cost (qwen поднял в P1); структурное поле `Degraded []string` в ответах GoT/retriever, показанное в UI/MCP; унификация логов на `slog` (Claude: fail-open без наблюдаемости = тихая деградация). Версионировать контракт ответа MCP/web.
6. **FTS5 вместо in-memory BM25 (M).** Убирает rebuild на запись/старте; FTS5 — основной candidate generator (qwen настоял: раньше ANN). За env-флагом с fallback.
7. **Config-гигиена (S/M) — только Claude Code.** Не 62, а 46 `KB_*`-переменных всего (проверено `grep`): 33 в `internal/config/env.go`, где базовая валидация формата (bool/int/enum) уже есть по каждой — реальный пробел не "без валидации", а отсутствие `kb config show`/дампа эффективного конфига и диапазонных пресетов; плюс 13 переменных, которые читаются напрямую коннекторами/подсистемами (`KB_DISCORD_TOKEN`, `KB_SOCKS_PROXY`, `KB_PLAN_*`...) без центральной валидации вовсе. `kb config show` + пресеты (`fast`, `quality`) + валидация диапазонов при старте; экспертиза из dragon-report закрепляется как пресеты.
8. **DRAGON-бенч: быстрые итерации (M) — только Claude Code.** `--persist-dir` с пропуском reindex по `doc_hashes` (механизм уже есть) + маленький fixed-сабсет для минутного sanity-прогона; история метрик по прогонам. Разблокирует эксперименты «второго раунда» из отчёта.

## P2 — качество и эксплуатация

9. **ANN-префильтр поверх FTS5 + entity-linking (L).** Cosine только внутри кандидатов; O(N) → O(K). Чистый Go (sqlite-vec — cgo). За env-флагом с fallback (все согласны: только после FTS5).
10. **Конкурентность SQLite (M) — только Claude Code.** `SetMaxOpenConns(1)` сериализует sync + дашборд: сначала профиль contention, затем WAL + read-only пул с одним writer'ом, с тестами восстановления после крэша.
11. **Кэш Ask (M) — только Claude Code.** Кэш по `hash(query + corpus_version + hash(KB_*))`, explicit invalidation; после п.7 и п.10. Fail-open path тоже перестанет платить дважды.
12. **Фидбек-луп 👍/👎 (S–M).** Персональный prior в RRF + размеченный eval-набор с провенансом разметки (qwen).
13. **Near-duplicate detection (M).** simhash/minhash при индексации.
14. **Миграция embedding-модели (M) — только Claude Code.** `kb reindex --embed-model=X --into=shadow.db` + сравнение метрик до cutover; сейчас смена модели = reindex с нуля.
15. **Guardrails (S–M).** PII-redaction перед внешним LLM как опция (не дефолт — оверхед); лёгкая защита от indirect prompt injection (контент из коннекторов в промптах); token-auth/rate-limit — P1, если дашборд выходит за loopback (qwen).

## P3 — отложенное

16. Инкрементальный Leiden (L), embedding/LLM-кэши (S), observability: trace ID + query plan (M).

## Секвенция

P0 (1–3, 2/3 параллельны и независимы) → P1 (4 → 5 → 6 → 7 → 8, 7/8 параллельны) → P2 (9 после 6; 10 → 11; 12/13/14/15 независимы) → P3.
