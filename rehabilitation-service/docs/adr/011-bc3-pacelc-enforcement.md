---
title: "ADR-011: Enforcement of PACELC PC/EL for BC#3 (rehabilitation-service)"
date: 2026-11-26
status: Accepted
---

# ADR-011: Enforcement of PACELC PC/EL for BC#3

> Локальная копия ADR сервиса. Канонический ADR-001 (`adr/001-pacelc-choices.md`)
> в `Reddetk/saymo-documentation` задаёт политику BC; этот документ фиксирует,
> какими механизмами политика обеспечивается в коде и какие гарантии даются.

## Context

ADR-001 выбирает для BC#3 **PC/EL**:

- при партиции — **Consistency**: структура программы не должна разъезжаться;
- в норме — **Latency**: чтение cache-first с eventual consistency ≤ 5 минут;
- запись — пессимистичные блокировки, чтение — singleflight.

Фактическое поведение до этого ADR расходилось с заявленной границей:

1. L2 TTL был 1 час: потерянная инвалидация давала staleness до 60 минут вместо 5.
2. L1 (5 минут на реплику) не инвалидировался на других репликах: при N инстансах
   чтение после записи могло отдавать старую версию до 5 минут.
3. Consumer при ошибке обработки останавливал партицию навсегда: offset не
   коммитился (C сохранена), но доступность терялась и лаг рос неограниченно.
4. Повторный `POST /admin/content/programs/:id/migrate-patients` создавал новый
   `migration_job_id` и второе событие: 202 не был идемпотентным.

## Decision

BC#3 остаётся **PC/EL**. Политика уточняется до уровня операций:

| Операция | P | E | Гарантия | Механизм |
|---|---|---|---|---|
| Create/Update/Delete program, lesson | C | C | линейность на программу; нет состояния без события и события без состояния | `SELECT ... FOR UPDATE` + outbox в одной транзакции |
| Инвалидация кэша после write | — | L | staleness ≤ 5 мин (жёстко), обычно < 1 с | L1 60 с, L2 5 мин, Redis Pub/Sub broadcast, локальный L1 purge |
| Public read (list/get/lesson) | A | L | read-your-writes на писавшей реплике; кросс-реплично ≤ 5 мин | L1 + L2 + singleflight + versioned keys |
| Presigned URL | A | L | ссылка не отдаётся, если сменился contentHash или осталось < 5 мин | ключ включает contentHash, cache 55 мин + проверка остатка |
| Delete (guard активных пациентов) | C | C | удаление запрещено при count > 0; неизвестный count → 503 | BC#4 S2S, fail-closed, breaker 5→30 с |
| Migration request | C | L | 202 = событие уже в outbox; одинаковый запрос при той же версии программы за UTC-сутки → тот же job id | UUIDv5(program, oldVersion, target, selection, sorted ids, дата) как PK outbox |
| Event consume | C | L | at-least-once + ровно один эффект; offset не коммитится при ошибке | retry 1→30 с, `processed_events` |
| Reference count (read/guard) | A | L | сходимость ≤ лаг событий; алерт при P95 > 60 с | consumer + gauge + alert |

### Механизмы

1. **Pub/Sub инвалидация L1** (`internal/adapter/redis/invalidation.go`,
   `ProgramQuery.Invalidate*` / `ApplyInvalidation`).
   Writer после commit чистит локальный L1 и L2, затем публикует scope
   (`all` или `program:<id>`) в `bc3:cache:invalidate`. Все реплики чистят
   только свой L1; L2 не трогают, чтобы не снести более свежее значение.
   Потеря сообщения ограничена TTL: L1 60 с, L2 5 мин (reconciliation path).
2. **Best-effort инвалидация** (`ProgramCommand.invalidate*`): ошибка кэша не
   превращает успешный commit в 5xx — ответ определяется только БД/outbox.
3. **Supervised consumer** (`internal/consumer/program_events.go`): обработка
   ретраится для **того же** сообщения (важно: `kafka-go.FetchMessage` сдвигает
   позицию читателя до `Commit`, поэтому повторный `ReadMessage` пропустил бы
   упавшее событие); backoff 1→30 с, offset коммитится только после успешного
   применения, commit ретраится отдельно.
4. **Идемпотентная миграция** (`CreateMigrationRequest`): детерминированный
   UUIDv5 (включая `oldVersion`) становится PK строки outbox; повторная вставка
   ловится по `outbox_pkey` и возвращает тот же `migrationJobId`. Новая таблица
   не нужна (и не создаётся: схема БД — зона человеческого ревью).
   Идемпотентность опирается на то, что строки outbox не удаляются; если появится
   retention/партиционирование, окно дедупликации должно быть не меньше UTC-суток.
5. **Наблюдаемость гарантий**: `event_processing_lag_seconds` +
   `EventProcessingLagHigh` (> 60 с), `redis_write_failures_total{operation="publish"}`
   + `CacheInvalidationPublishFailures`.

## Consequences

- Худшая допустимая staleness чтения — 5 минут, типичная после записи — доли
  секунды (pub/sub) при сохранении L1-латентности.
- При падении Redis сервис не отдаёт трафик (`/ready` уже проверяет Redis),
  поэтому деградация не маскируется.
- Дубликат migration-запроса больше не создаёт второе событие; повтор в другие
  UTC-сутки создаёт новый job (осознанно: миграцию можно запросить снова).
- Consumer не теряет события и не «умирает»: при недоступной БД он ретраит,
  лаг виден в метрике и алерте.
- Схема БД не менялась.

## Docs deviation

`classifications/RehabilitationContentManagement.md` задаёт «L1 TTL 5 min + L2 Redis TTL 1h».
Это противоречит границе eventual consistency ≤ 5 мин из ADR-001. Приоритет отдан
ADR-001: L1 60 с, L2 5 мин. Отклонение осознанное; при синхронизации ADR в
`Reddetk/saymo-documentation` classification нужно обновить, иначе документы расходятся с кодом.

## Rejected alternatives

- **Читать версию из Redis на каждый read** — добавляет RTT и ломает EL.
- **Отказаться от L2 или снизить TTL до секунд** — рост нагрузки на PostgreSQL.
- **Redis SETNX как idempotency key** — Redis не source of truth, противоречит PC.
- **Новая таблица `migration_requests`** — лишняя сущность и миграция; PK outbox
  уже даёт уникальность и durability.
- **Останавливать consumer при ошибке (старое поведение)** — сохраняет C, но
  навсегда убивает доступность и скрывает лаг.
- **Синхронная миграция** — противоречит контракту 202.

## Testing

- `TestInvalidationPublishesScopeAndClearsL1`, `TestApplyInvalidationClearsOnlyLocalL1`,
  `TestInvalidationPublishFailureDoesNotFailWrite` (usecase).
- `TestRunRetriesProcessingErrorWithoutCommitting` (consumer, `-race`).
- `TestMigrationRequestIsIdempotentWithinUTCDay` (integration, PostgreSQL).
- E2E миграции: повторный запрос возвращает тот же `migration_job_id`.
