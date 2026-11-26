# progress-service — BC#4 Progress Tracking and Assessment

Сервис прогресса пациентов SayMo: отслеживание завершённых уроков, доступные уроки по
тарифному расписанию, ответы пациентов (append-only), админ-просмотр и deletion guard для BC#3.

**PACELC:** PA/EL — доступность при разделении (пациент отправляет ответы даже при недоступности
других BC), latency в норме (cache-first чтение).
**Aggregate Root:** `PatientProgress` (внутри: `ActiveProgram`, `AccessibleLesson`, `LessonAnswer`,
`TariffSnapshot`, `ProgressSnapshot`).

## Источники истины

| Что | Документ |
|---|---|
| Границы BC, PACELC, зависимости | `Reddetk/saymo-documentation`: `BC.md#bc4-progress-tracking-and-assessment` |
| Доменная спецификация, инварианты 1–11, потоки | `classifications/ProgressTrackingAndAssessment.md` |
| HTTP-контракты (поля, коды) | `endpoints.md#bc4` |
| События, payload, consumer'ы | `events.md#bc4`, `EventDataFlow.md#bc4-progress-tracking-and-assessment` |
| Термины | `glossary.md` |
| Метрики/алерты/логи/трейсы | `Observability.md#bc4-progress-tracking-and-assessment` |
| DTO на границах портов | `adr/007-port-owned-dto-boundary.md` |
| Model B (ответ ≠ завершение) | `adr/002-model-b-answer-completion.md` |
| PACELC | `adr/001-pacelc-choices.md` |
| Redis-топология | `deployment/adr-redis-topology.md` |
| Миграции, Taskfile, Docker, Testing | `deployment/*.md` |

## Архитектура (AS-IS по документации)

Гексагон: `internal/core` (домен) ← `internal/port` (in/out DTO) ← `internal/adapter`
(primary HTTP / secondary postgres, redis, BC#3 HTTP, S3, kafka). Зависимости только внутрь.
DTO на границах портов принадлежат пакетам `port/in` и `port/out` (ADR-007), доменные типы
наружу не выходят. DI — ручная сборка в `cmd/server/main.go` (composition root), без глобальных
синглтонов.

```mermaid
classDiagram
    class PatientProgress {
        +UUID accountId
        +int completedLessons
        +int totalLessons
        +int completionPercent
        +TariffSnapshot tariff
        +ActiveProgram[] programs
        +AccessibleLesson[] accessible
        +completeLesson(now) 
        +unlock(now) bool
    }
    class ActiveProgram {
        +UUID programId
        +string status
        +time activatedAt
        +time? completedAt
        +time? pausedAt
        +int completionPercent
    }
    class AccessibleLesson {
        +UUID lessonId
        +string lessonType
        +int orderPosition
        +time grantedAt
        +time? revokedAt
    }
    class LessonAnswer {
        +UUID answerId
        +UUID lessonId
        +string mediaType
        +string payload
        +time submittedAt
    }
    class TariffSnapshot {
        +int weekBitmap
        +string timezone
        +time? validUntil
        +time activatedAt
    }
    class ProgressSnapshot {
        +UUID programId
        +int completionPercent
        +string reason
    }
    PatientProgress "1" --> "1" TariffSnapshot
    PatientProgress "1" --> "*" ActiveProgram
    PatientProgress "1" --> "0..1" AccessibleLesson
    PatientProgress "1" --> "*" LessonAnswer
    PatientProgress "1" --> "*" ProgressSnapshot
```

AS-IS численно: до реализации сервиса — 0 эндпоинтов, 0 таблиц, 0 событий, 0 метрик.

### Последовательность: завершение урока (Model B, POST /complete)

```mermaid
sequenceDiagram
    autonumber
    participant PWA
    participant MW as JWT+Idempotency
    participant H as LessonHandler
    participant S as ProgressService
    participant DB as PostgreSQL
    participant O as Outbox
    participant R as Redis L2
    PWA->>MW: POST /progress/lessons/{id}/complete (Idempotency-Key)
    MW->>MW: RS256, exp/iat/iss/aud ±30s, jti/rev (L1→L2)
    MW->>H: AuthContext{accountId, role}
    H->>S: CompleteLesson(cmd)
    S->>DB: guard 409/422/403 + current progress
    S->>DB: BEGIN; INSERT lesson_completions; UPDATE patient_progress;
    S->>DB: revoke lesson; grant next (computed unlock)
    S->>O: LessonCompleted (+ ProgramCompleted при 100)
    S->>DB: COMMIT
    S->>R: invalidate progress/accessible/active + lesson_completed TTL 30d
    H-->>PWA: 200 {completionPercent, nextLessonId}
```

### Последовательность: событие тарифа (Kafka consumer)

```mermaid
sequenceDiagram
    autonumber
    participant K as Kafka saymo.billing.events
    participant C as TariffConsumer
    participant DB as PostgreSQL
    participant O as Outbox
    participant R as Redis L2
    K->>C: TariffPlanActivated{accountId, tariffSnapshot}
    C->>DB: BEGIN; idempotency_keys(event_id)
    C->>DB: UPSERT TariffSnapshot, пересчёт accessible_lessons
    C->>O: ProgramAccessChanged
    C->>DB: COMMIT; commit offset
    C->>R: invalidate progress/accessible/active
```

## Реализовано (шаги плана, AS-IS → TO-BE)

| Шаг | AS-IS | TO-BE (проверено) | Статус |
|---|---|---|---|
| 1. Окружение и стенд | нет каталога/инфры | goca-проект; compose: postgres 5434, redis-bc4 6381, redis-blacklist 6380, kafka 9094; `/health`, `/health/live`, `/health/ready`, `/metrics`; миграции up/down/up | [x] |
| 2. Домен и схема | 0 таблиц | 9 таблиц + 6 индексов, парные миграции `000001..000015`; unit-тесты инвариантов + DB integration | [x] |
| 3. Auth/RBAC/ownership | 0 защищённых ручек | RS256+JWKS (TTL 1h, unknown kid → 1 fetch), ±30s, L1→L2 revocation, fail-closed 503; 401/403/404 | [x] |
| 4. Программы: activate/read/resume | 0 ручек | `GET /progress/me`, `POST /progress/me/activate`, `POST /progress/programs/{id}/resume`; 409/404/400/503 | [x] |
| 5. Ответы | 0 ручек | `POST .../answers/presign`, `POST .../answers`; append-only, 409/403/422 | [x] |
| 6. Завершение и unlock | 0 ручек | `POST .../complete`; FLOOR, Rules 1–3, 409/422/403, 100% → ProgramCompleted | [x] |
| 7. Сложность и админ | 0 ручек | `PATCH /progress/difficulty`, 5 `/admin/*` ручек, deletion guard | [x] |
| 8. GDPR self-delete | 0 ручек | `DELETE /progress/me`; 400 без header, 204, анонимизация ответов | [x] |
| 9. Kafka + outbox | 0 consumer'ов/событий | 14 consumer-типов, 8 событий через outbox, DLQ, идемпотентность 7d в TX | [x] |
| 10. Наблюдаемость + docs | 0 метрик | 15+ метрик BC#4, `/metrics`, OTel, структурные логи, swagger `docs/` | [x] |

Остаточные ограничения перечислены в разделе «Известные ограничения».

## HTTP API (реализовано)

Пациент (JWT, `token.sub == accountId`):

```
GET    /progress/me
POST   /progress/me/activate                    (Idempotency-Key)
DELETE /progress/me                             (X-Confirmation: delete-my-progress)
POST   /progress/lessons/{lessonId}/answers/presign
POST   /progress/lessons/{lessonId}/answers     (Idempotency-Key)
POST   /progress/lessons/{lessonId}/complete    (Idempotency-Key)
PATCH  /progress/difficulty                     (Idempotency-Key)
POST   /progress/programs/{programId}/resume    (Idempotency-Key)
```

Админ (`role=administrator`):

```
GET    /admin/progress/{accountId}
GET    /admin/answers/{accountId}               (?lessonId&mediaType&limit&offset)
GET    /admin/answers/{accountId}/{lessonId}
PATCH  /admin/progress/{accountId}/difficulty
```

Service-to-service (BC#3 → BC#4, обязательный `X-Service-Token`, значение из `INTERNAL_SERVICE_TOKEN`):

```
GET /admin/progress/program/{programId}/version/{version}/active-patients
```

Служебные: `GET /health`, `GET /health/live`, `GET /health/ready`, `GET /metrics`, `GET /swagger/index.html`.

Коды ошибок скопированы из `endpoints.md`: `no_active_program`, `program_already_active`,
`lesson_already_completed`, `lesson_not_accessible`, `no_answer_submitted`,
`no_program_for_classifier`, `program_catalog_unavailable`, `program_not_paused`,
`current_program_not_completed`, `program_no_longer_exists`, `confirmation_required`,
`invalid_payload`, `invalid_difficulty`, `invalid_path_param`.

## События

Подписки (consumer group `progress-service`, топики `saymo.identity.events`,
`saymo.billing.events`, `saymo.content.events`):

`AccountRegistered` → авто-активация по classifier; `TariffPlanActivated/Changed/DeActivated`,
`SubscriptionActivated/Changed/Suspended/Resumed/Renewed/Expired/DeActivated` → TariffSnapshot и доступ;
`MigrationRequested` → batch-миграция версии; `LessonAddedToProgram` → пересчёт total/percent;
`AccountDeleted` → GDPR-анонимизация ответов и удаление агрегата.

Тариф приходит раньше регистрации: снимок сохраняется в `account_tariffs` и используется при
`AccountRegistered`, поэтому порядок событий не приводит к потере доступа.

Публикация через outbox (`saymo.progress.events`): `ProgramActivated`, `LessonAnswersSubmitted`,
`LessonCompleted`, `ProgramCompleted`, `ProgramDifficultyChanged`, `ProgramAccessChanged`,
`MigrationCompleted`, `ProgressDeleted` (GDPR audit). Envelope содержит обязательные
`event_id`, `event_type`, `event_version`, `correlation_id`, `occurred_at`, `payload`.

## Тестовый стенд и проверки

Порты (конфликтуют с уже запущенными сервисами этой машины, поэтому сдвинуты):

| Компонент | Порт |
|---|---|
| progress-service | 8082 |
| postgres | 5434 |
| redis-bc4 / redis-blacklist | 6381 / 6380 |
| kafka (KRaft) | 9094 |
| BC#3 stub (`cmd/bc3mock`) | 8083 |
| JWKS mock (`cmd/jwksmock`) | 8098 |

```powershell
# 1. инфраструктура (сеть создаётся один раз)
docker network create saymo-blacklist
docker compose --env-file .env.local up -d postgres redis-bc4 redis-blacklist kafka

# 2. ключи и миграции
go run ./cmd/genrsa -out ./testdata/keys
Get-Content .env.local | ForEach-Object { if ($_ -match '^\s*([^#][^=]*)=(.*)$') { Set-Item "Env:$($matches[1].Trim())" $matches[2].Trim() } }
go run ./cmd/migrate/main.go -action up

# 3. поднять сервис и заглушки
go run ./cmd/server/main.go        # отдельный терминал
go run ./cmd/bc3mock/main.go -addr :8083
go run ./cmd/jwksmock/main.go -addr :8098

# 4. health и метрики
curl.exe http://127.0.0.1:8082/health/ready
curl.exe http://127.0.0.1:8082/metrics | Select-String lessons_completed_total
# deletion guard требует заголовок:
curl.exe -H "X-Service-Token: local-service-token" http://127.0.0.1:8082/admin/progress/program/<programId>/version/1/active-patients

# 5. токен и smoke
$T = (go run ./cmd/tokenmint/main.go -account 11111111-1111-1111-1111-111111111111).Trim()
curl.exe -H "Authorization: Bearer $T" http://127.0.0.1:8082/progress/me

# 6. тесты
go vet ./... ; go build ./... ; go test ./... -count=1 -race
$env:KAFKA_BROKERS='localhost:9094'; go test -tags=integration ./internal/adapter/secondary/kafka -count=1 -v
$env:POSTGRES_DSN='postgres://saymo:changeme@localhost:5434/saymo_progress?sslmode=disable'; go test -tags=integration ./internal/adapter/secondary/postgres -count=1 -v
```

E2E-прогон по контрактам `endpoints.md` и независимое ревью: `.qa/e2e-bc4.md`, `.qa/review-bc4.md`.

Тесты, выросшие из CSV-фикстур (`testdata/fixtures/*.csv`, 175 строк):
- `TestFixtureInventory` — парсит все 175 строк и проверяет, что каждый ожидаемый `error` входит в известный контрактный набор.
- `TestHTTPFixtures` — HTTP-раннер: 105 строк исполняются на `httptest` с in-memory миром (`internal/testkit`), реальными сервисами и роутером; проверяются статус и точный `error`. Остальные строки требуют другого состояния (completed/paused/creative, S3-ключи, недоступная БД) и покрыты service-level и E2E тестами.
- `TestSwitchBackABAVerifiesUpsert` — easy→medium→easy (upsert вместо 500).
- `TestParallelSubmitAndComplete` — 40 итераций гонки submit/complete; принятый ответ не может быть позже завершения.
- `TestDeleteEmitsProgramCompletedOnlyForActiveProgram` — GDPR не декрементит BC#3 за уже завершённую программу.
- `TestAccountDeletedEventIsIdempotent` — `AccountDeleted` анонимизирует ответы и не повторяется.
- `TestTariffBeforeRegistrationIsApplied` — тариф до `AccountRegistered` применяется при активации.

## Definition of Done (каждый шаг)

`go vet ./...`, `go build ./...`, `go test ./... -count=1 -race` зелёные; HTTP-E2E по контрактам
`endpoints.md` зелёный; план сдвинут; миграции парные; `docs/` перегенерирован.

## Гейты перед кодом (AGENTS.md)

1. **Что делаю.** Реализую BC#4 progress-service по спецификации, начиная с окружения и
   домена; поведение и поля копирую из docs, не «улучшаю».
2. **Скилы.** `golang-code-review` (ревью шага), `diagram-design` (диаграммы выше),
   `goca` (`init` + `goca doctor/analyze`), `browser`/`curl` для E2E.
3. **Последовательность.** Шаги 1→10; внутри шага: фикстуры (чистый субагент) → красные тесты →
   код → `go vet/build/test` → независимые reviewer и HTTP-E2E → следующий шаг.
4. **Аудит SOLID/KISS/ООП/DDD.**
   - SRP: агрегат считает прогресс/доступ; сервисы оркестрируют (progress, lesson, admin, events,
     token); адаптеры — I/O.
   - OCP: новый тип события добавляется хендлером consumer'а, домен не меняется.
   - LSP/ISP: узкие порты (`Repository`, `Tx`, `OutboxRepository`, `ContentCatalog`, `Cache`,
     `IdempotencyStore`, `ObjectStorage`, `JWKSProvider`, `RevocationStore`); моки только своих
     интерфейсов.
   - DIP: core зависит от портов, не от pgx/redis/gin; DI в `main`.
   - KISS: без CQRS/медиатора; один агрегат = одна транзакция; unlock вычисляется лениво.
   - DDD: `PatientProgress` — AR; `ActiveProgram`/`AccessibleLesson`/`LessonAnswer` — entity;
     `TariffSnapshot`/`CompletionPercent` — VO; инварианты в домене, не в handler'ах.
   - Границы: ответы append-only, отправка ответа не двигает прогресс (ADR-002),
     BC#4 никогда не читает тарифы из BC#2, контент — только через BC#3.

## Отклонения от шаблонов и их причины

| Решение | Причина |
|---|---|
| `goca init --template enterprise`, затем ручная гексагональная раскладка (`internal/{core,port,adapter}`) | Шаблон генерирует `gorilla/mux` + GORM/sqlite; docs и ADR-007 требуют gin + pgx + port-owned DTO. Код, которого генератор не покрывает, помечен `// goca:manual`. |
| HTTP-порт 8082, postgres 5434, redis-bc4 6381, Kafka 9094 | 8080/5433/6379/9092 заняты `identy-service`, локальным WordPress и Kafka другого сервиса. |
| Revocation L1→L2, без L3 PostgreSQL | L3 — БД BC#1; прямой доступ к ней запрещён границей BC. Redis-blacklist недоступен → 503 fail-closed (ADR Redis-топология). |
| Presign PUT без `content-length-range` | PUT-presign в S3 не поддерживает policy-условия; лимит 10MB не enforceable на этапе выдачи URL. Валидация mediaType/URL выполняется при submit. |
| `INTERNAL_SERVICE_TOKEN` для deletion guard | В `endpoints.md` сказано «no user JWT», конкретный механизм не задан; выбран обязательный shared secret (пустое значение не допускается — иначе guard раскрывает `patientIds`). |
| Default TariffSnapshot `weekBitmap=127/UTC` при полном отсутствии тарифа | `endpoints.md` разрешает «defaults from tariff catalog», `deployment/testing.md` фиксирует дефолт billing-stub `127/UTC`. Действует только в `local`/`test`; в `prod` дефолт `0` (доступ не открывается до реального события тарифа), настраивается `TARIFF_DEFAULT_WEEK_BITMAP`. |
| Composite idempotency key `eventID:accountID` для `MigrationRequested` | Позволяет продолжить batch после падения на середине без повторной обработки уже мигрированных пациентов. |

## Известные ограничения

1. `GET /admin/progress/{accountId}.email` возвращается пустым: BC#4 не хранит PII BC#1 и не имеет
   права читать его БД; административный UI джойнит по `accountId`.
2. Ответы пациентов хранятся бессрочно (медицинские записи) и не чистятся автоматически; GDPR-удаление
   анонимизирует `payload`.
3. Ссылки S3 в ответах проверяются по префиксу бакета; фактический размер объекта (10MB) не
   проверяется — ограничение PUT-presign.
4. При недоступности Redis идемпотентность HTTP fail-open (ответы остаются доступны по PACELC PA),
   но дубли ответов возможны; `complete` защищён уникальным `lesson_completions`.
5. Kafka-доставка at-least-once; дубли отсекаются таблицей `idempotency_keys` в транзакции.
   Транзиентные ошибки consumer'а ретраятся до 5 минут без commit offset; после окна сообщение
   уходит в DLQ, и offset коммитится только при подтверждённой записи в DLQ (иначе повтор).

## Локальный запуск

```powershell
Copy-Item .env.example .env.local   # затем скопировать значения в окружение
docker compose --env-file .env.local up -d postgres redis-bc4 redis-blacklist kafka
go run ./cmd/migrate/main.go -action up
go run ./cmd/server/main.go
curl.exe http://127.0.0.1:8082/health/ready
```

`cmd/server` не читает `.env` (как и в identy-service): переменные должны быть в окружении.
