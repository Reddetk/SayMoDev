# rehabilitation-service — BC#3 Rehabilitation Content Management

Сервис управления реабилитационным контентом платформы SayMo (PWA реабилитации после инсульта).

- **BC:** BC#3 «Rehabilitation Content Management».
- **Источники истины:** `Reddetk/saymo-documentation` → `classifications/RehabilitationContentManagement.md`, `endpoints.md#bc3`, `events.md#bc3`, `EventDataFlow.md`, `Observability.md`, `adr/001`, `adr/004`, `adr-007`, `adr/008`, `deployment/*` (порядок при конфликте — как в `../AGENTS.md`).
- **Имя каталога/модуля:** постановка задачи называет сервис `rehabilitation-service`; `deployment/README.md` называет BC#3 `content-service`. Каталог и module path следуют постановке: `github.com/Reddetk/SayMoDev/rehabilitation-service`. HTTP-контракты, порты (8003, metrics 9003) и имена событий — из docs, без переименований.
- **Порт:** HTTP `8003`, Prometheus `/metrics` `9003` (`Observability.md`, таблица портов).
- **Стек:** Go 1.26, gin, pgx/v5, golang-migrate, Redis (go-redis), Kafka, AWS SDK v2 (S3/Yandex Object Storage), Prometheus, OpenTelemetry, zap (`log/slog` допускается только вне request-path; в проекте принят zap как в docs).

> Сгенерированный goca-код — основа, а не догма. Всё, что генератор не покрывает, помечено `// goca:manual — <причина>`; ручные правки не должны затираться повторной генерацией.

---

## План реализации (пайплайн AGENTS.md)

**Текущий шаг: S5 — admin write API + S3.** S1 закрыт 2026-11-25, S2 закрыт 2026-11-25 (домен 94.5% coverage, миграции 000004–000014), S3 закрыт 2026-11-26 (PASS ревьюера), S4 — реализован и E2E-зелёный (7/7), ждёт ревьюера. Измерено 2026-11-25: Go 1.26.0 (windows/amd64), Docker 29.9.3, Docker Compose v5.7.0, goca v1.27.0, swag v1.16.4; `task` и `golangci-lint` не установлены.

### AS-IS: измеренные цифры (2026-11-25, до S1)

| Метрика | AS-IS | Чем измерено |
|---|---|---|
| Каталог сервиса | создан `goca init --template enterprise --database postgres` (v1.27.0) | `goca init` |
| Go-файлов | 3 (`cmd/server/main.go`, `pkg/config`, `pkg/logger`) | `Get-ChildItem -Recurse -File` |
| HTTP-маршруты | 3: `/health`, `/health/ready`, `/health/live` | `cmd/server/main.go` |
| `/health` семантика | всегда `200`, статус `degraded` не меняет код ответа | `healthCheckHandler` |
| `/ready` | проверяет только PostgreSQL | `readinessHandler` |
| `/metrics` | 0 | нет обработчика и клиента Prometheus |
| Доменные таблицы | 0 | `migrations/001_initial.up.sql` — пустой шаблон |
| Миграции | 1 пара (пустая) + GORM `AutoMigrate` при старте | `main.go` |
| Docker compose | 2 сервиса (`rehabilitation-service`, `database`), без healthcheck у сервера, пароли в файле | `docker-compose.yml` |
| Redis / Kafka / S3 / Jaeger | 0 / 0 / 0 / 0 | compose |
| JWT-валидация / RBAC | 0 | нет middleware, `JWT_SECRET` в `.env.example` (неверная модель: BC#3 валидирует RS256 по JWKS) |
| Kafka-консьюмеры / outbox-publisher | 0 / 0 | нет кода |
| Тесты | 0 | `go test ./... -count=1` — `[no test files]` |
| `go build ./...`, `go vet ./...` | зелёные | локальный запуск |
| Покрытие | 0% | нет тестов |

### AS-IS: диаграмма (сгенерированный каркас)

```mermaid
flowchart LR
    C[Client] -->|GET /health, /health/ready, /health/live| S[cmd/server<br/>net/http + gorilla/mux]
    S -->|GORM AutoMigrate + Ping| PG[(PostgreSQL<br/>database:5432)]
    S -.->|нет| R[(Redis)]
    S -.->|нет| K[(Kafka)]
    S -.->|нет| S3[(S3)]
```

Sequence AS-IS (`GET /health/ready`):

```mermaid
sequenceDiagram
    participant C as Client
    participant H as readinessHandler
    participant PG as PostgreSQL
    C->>H: GET /health/ready
    H->>PG: sqlDB.PingContext (2s timeout)
    alt ping ok
        H-->>C: 200 "Ready"
    else ping fail
        H-->>C: 503 "Database not ready"
    end
```

### AS-IS → TO-BE по шагам

| Шаг | Цель | AS-IS → TO-BE (цифры) | Docs / ADR |
|---|---|---|---|
| **S1** | Окружение, тестовый стенд, health-check каждой среды | compose 2 → 8 сервисов (postgres, redis-bc3, kafka, s3mock, s3init, jaeger, migrate, server); `/health`+`/ready`+`/metrics` 2 → 3 (метрики на :9003); миграции 1 пустая → 3 инфраструктурные пары; тесты 0 → 9 test-функций / 40 PASS-записей + 5 E2E; GORM AutoMigrate → golang-migrate; 3 env-файла | deployment/*, Observability.md |
| **S2** | Домен Program/Lesson + миграции | таблицы 0 → 2 (`programs`, `program_lessons`); миграции 3 → 14 пар (PK по версии, 5 индексов, CHECK-и домена); инварианты: classifier unique/immutable, ≥1 lesson, order_position без дыр, лимит 200; 107 PASS-записей; покрытие домена 94.5% | classification BC#3 §1–4 |
| **S3** | JWT (RS256/JWKS) + RBAC + L1/L2 revocation | 3 CSV-фикстуры (62 кейса), auth coverage 76.6%; 401/403 ветки; JWKS-кэш TTL 1h, timeout 2s, fail-closed; RBAC `administrator`; fail-closed группы при `Auth == nil` (503) | classification §Authentication, ADR-003/004 |
| **S4** | Read API каталога | 0 → 3 GET (`/content/programs`, `/{id}?version`, `/{id}/lessons/{lessonId}`); L1 5min + L2 Redis 1h + singleflight; presigned URL TTL 1h / кэш 55min с перегенерацией при остатке ≤5min; 15 fixture-кейсов, handler 85.5%, E2E 7/7 | endpoints.md#bc3, classification §HTTP |
| **S5** | Admin write API + S3 | 0 → 5 write-маршрутов (POST/PATCH/DELETE program, POST/PATCH/DELETE lesson); dedup SHA256; append не инкрементирует версию, insert_after_position инкрементирует; лимит 200; guard удаления через BC#4; ownership/403 для урока; инвалидация `programs:all`, `program:*`, `lesson:*`, `presigned_url:*` | endpoints.md#bc3, classification §1–5 |
| **S6** | Kafka: outbox publisher + consumers | события 0 → 8 типов (`ProgramCreated`, `ProgramUpdated`, `ProgramDeleted`, `LessonAddedToProgram`, `LessonInsertedIntoProgram`, `LessonUpdated`, `LessonContentChanged`, `LessonRemovedFromProgram`); consumers 0 → 2 (`ProgramActivated`, `ProgramCompleted`), throttle 50 events/sec, idempotency TTL 7d | events.md#bc3, EventDataFlow T1/T2 |
| **S7** | Миграция пациентов | 0 → 1 endpoint `POST /admin/content/programs/{programId}/migrate-patients`; 202 + `migration_job_id`; rate limit 10/min | endpoints.md#bc3, classification |
| **S8** | Наблюдаемость, swagger, E2E | метрик 0 → семейства из Observability.md BC#3; `/metrics` :9003; `docs/` swagger; E2E по всем контрактам BC#3 | Observability.md |

Definition of done каждого шага: `go vet ./...`, `go build ./...`, `go test ./... -count=1 -race` зелёные, E2E зелёный, `docs/` swagger перегенерирован, этот план сдвинут, миграции парные.

---

## S1 — окружение и тестовый стенд (закрыт 2026-11-25)

### Подшаги S1

1. **S1.1 Конфигурация и fail-fast.** Единый `pkg/config`: env-only, обязательные переменные без дефолтов в `prod`, `.env.local` / `.env.test` / `.env.example` (`.env.prod` не коммитится). Конфиг валидируется на старте; при невалидном конфиге сервер не стартует.
2. **S1.2 Инфраструктура.** `docker-compose.yml`: postgres 16 (host 5435), redis-bc3 (6382), Kafka (KRaft, 9092), s3mock (9005/9006, S3-совместимый стенд для Yandex Object Storage), Jaeger (16686/4317), migrate (one-shot), server (8003/9003); shared `redis-blacklist` — через `REDIS_BLACKLIST_ADDR` (на этой машине 6383). Healthcheck у каждой зависимости, `depends_on: service_healthy`, сервер — после `migrate: service_completed_successfully`.
3. **S1.3 Миграции.** `cmd/migrate/main.go` (golang-migrate, флаги `-action up|down|force -version N`), пары `000001_create_outbox`, `000002_create_idx_outbox_unsent`, `000003_create_processed_events` с `BEGIN/COMMIT` и `DROP ... IF EXISTS`. GORM AutoMigrate удаляется.
4. **S1.4 Health / readiness / metrics.** `/health` (liveness, 200 всегда при живом процессе), `/ready` (200 только если PostgreSQL + Redis + Kafka writer + S3 bucket доступны; 503 + JSON со списком), `/metrics` на отдельном порту 9003 (Prometheus registry + `service_up`, `build_info`). Проверки с таймаутом 2s.
5. **S1.5 Базовый HTTP-сервер.** gin, graceful shutdown, request-id/trace, структурированный zap-лог, `X-Content-Type-Options`/`X-Frame-Options`, CORS до auth (для будущих шагов), раздельные admin/public группы (пока пустые).
6. **S1.6 Taskfile + Dockerfile.** `Taskfile.yml` с `infra:up/down`, `migrate:up/down/force`, `run`, `test:unit`, `test:integration`, `test:e2e`, `build`, `swagger`; `Dockerfile.server` (golang:1.26-alpine → distroless) и `Dockerfile` (образ migrate-бинарника).
7. **S1.7 TDD.** Фикстуры от чистого субагента (CSV: health/ready/metrics, границы env), красные тесты, затем реализация. E2E только через HTTP.

### TDD: фикстуры (от чистого субагента, без контекста)

Файлы: `testdata/fixtures/health.csv`, `testdata/fixtures/ready.csv`, `testdata/fixtures/config.csv`.
Покрываемые ветки: 200 (все зависимости живы), 503 (PostgreSQL недоступен / Redis недоступен / S3 недоступен), 405 (метод), 404 (путь), пустые/unicode/максимальные значения env, невалидный порт, отрицательные таймауты, отсутствующая обязательная переменная в prod. Каждая строка CSV имеет `case_id`, вход, ожидаемый HTTP-код и ожидаемый инвариант.

### Гейт S1 — закрыт

- [x] `go vet ./...`, `go build ./...`, `go test ./... -count=1 -race` — зелёные (лог: `BUILD=0 VET=0 TEST=0`)
- [x] `docker compose --env-file .env.local up -d` — postgres, redis-bc3, kafka `healthy`; s3init завершился, бакет создан
- [x] `GET :8003/health` → 200 `{"status":"ok",...}`; `GET :8003/ready` → 200 `{"status":"ready","checks":{"kafka":"ok","postgres":"ok","redis":"ok","s3":"ok"}}`; после `docker stop redis` → 503, после `start` → 200
- [x] `GET :9003/metrics` → 200, `service_up 1`, `build_info{commit="none",version="dev"} 1`
- [x] `go run ./cmd/migrate -action up|down` → `version=3 dirty=false` (up → down → up)
- [x] `POST :8003/health` → 405, `GET :8003/healthz` → 404
- [x] CORS: preflight `OPTIONS` с разрешённым Origin → 204 + `Access-Control-Allow-Origin` до JWT; неизвестный Origin заголовка не получает
- [x] `X-Request-ID` и `X-Trace-ID` (из W3C `traceparent` или сгенерированный) в ответе и в логах
- [x] swagger: `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml`; `/swagger/index.html` в роутере; `go build/vet/test ./...` включают пакет `docs`
- [x] E2E (`go test -tags=e2e ./test/e2e/...`) — 5/5 PASS
- [x] субагент-ревьюер: три раунда, финальный вердикт PASS (docs-dependency, дедлайн Kafka-пробы, S1.5, compose/README)

Отклонения от docs, зафиксированные при закрытии S1:

1. **Локальный S3 — `adobe/s3mock` (пин по digest), не MinIO**: образы `minio/minio` и `minio/mc` недоступны из реестра на этой машине (`pull access denied`). Контракт S3 API (path-style, HeadBucket, presigned GET) тот же; в prod — Yandex Object Storage.
2. **Порт Redis — 6382, не 6381**: 6381 занят запущенным соседним `progress-service-redis-bc4` (BC#4). Конфликт портов, не контрактов.
3. **Shared `redis-blacklist` — 6383 на этой машине**: 6380 занят blacklist-инстансом BC#4. В S1 не используется; адрес вынесен в env.
4. **`go 1.26.0` в go.mod**: `go mod tidy` поднял директиву под фактический toolchain 1.26.0; `Dockerfile*` используют `golang:1.26-alpine` (docs говорят 1.25 — обновится вместе с docs/ADR при следующем шаге).
5. **OTel-экспорт (Jaeger :4317) — S8**: `JAEGER_OTLP_ENDPOINT` зарезервирован, но SDK не инициализируется. Корреляция S1 обеспечена `trace_id` из `traceparent`/генерацией, без экспортёра.

### S2 — домен и миграции (закрыт 2026-11-25)

**Цель:** доменная модель BC#3 без HTTP/Kafka, миграции доменных таблиц, красные тесты из CSV чистого субагента.

Подшаги:

1. **S2.1 Value objects.** Тип `Classifier` (difficulty `easy|medium|hard`, aphasia_type структурно: non-empty, ≤50, без обрамляющих пробелов). Остальные инварианты — inline-валидаторы без отдельных типов (KISS): title 1..255 рун, OrderPosition 1..200, ContentHash `^[0-9a-f]{64}$`, version ≥1. Sentinel-ошибки — в `internal/domain/errors.go`.
2. **S2.2 Сущности.** `Lesson` (title, description, content_url, content_hash, file_size_bytes>0, mime_type=`application/pdf`, uploaded_by) и агрегат `Program` (id, title, classifier, version, lessons, active_reference_count).
3. **S2.3 Поведение агрегата.** `NewProgram` (≥1 урок, позиции 1..N без дыр/дублей или автоназначение), `AppendLesson` (версия не меняется), `InsertLessonAfter(K)` (валиден только K∈[1,N-1]: версия+1 и сдвиг K+1..N; K≥N → 400 по контракту), `Rename` (классификатор не принимается), `IncrementRefCount`/`DecrementRefCount` (пол 0). Лимит 200 уроков.
4. **S2.4 Миграции.** `000004_create_programs`, `000005_create_program_lessons`, `000006_create_uniq_programs_classifier`, `000007_create_uniq_lessons_version_position`, `000008_create_idx_lessons_content_hash`, `000009_change_program_lessons_pk` (PK `(program_id, program_version, order_position)` — grandfathering), `000010_create_idx_lessons_id`, `000011_add_program_checks`, `000012_add_lesson_checks`, `000013_drop_uniq_lessons_version_position`; пары up/down, `BEGIN/COMMIT`.
5. **S2.5 Порты и pgx-адаптер — перенесено в S4.** Репозиторий без потребителя (usecase) — преждевременная абстракция; S2 закрывает домен и схему. DTO на границах (ADR-007) и `RestoreLesson` (восстановление с внешним `lesson_id` для grandfathering) появятся вместе с первым usecase в S4.
6. **S2.6 TDD.** CSV от чистого субагента: `testdata/fixtures/program.csv` (35 кейсов) и `lesson_ops.csv` (15 кейсов). Сначала красные тесты (compile fail), затем реализация.

**Гейт S2 — закрыт:** `go build/vet/test -race` зелёные; `go test ./internal/domain -cover` = **94.5%** (≥85%); миграции `up → down×4 → up` и `up → down → up` → `version=14 dirty=false`; 107 PASS-записей; интерпретации контракта зафиксированы ниже.

**Решения S2, сверенные с docs:**

| # | Вопрос | Решение S2 | Основание |
|---|---|---|---|
| 1 | `insert_after_position` = последняя позиция | **400** `insert_after_position`; append — отдельная операция | classification §4 Error Cases: `K >= total_lessons → 400` |
| 2 | Версия при `insert_after_position` | ++версия при валидном K (< N) | endpoints.md: «version incremented only if insert_after_position provided» |
| 3 | Лимит длины title | 255 рун (255 valid / 256 invalid) | docs не задают число; 255 зафиксировано в домене и БД CHECK |
| 4 | Допустимые `difficulty` | `easy`, `medium`, `hard` (точное совпадение, без trim) | endpoints.md + use-cases |
| 5 | Допустимые `aphasia_type` | структурно: non-empty, ≤50, без обрамляющих пробелов (enum не вводится) | ADR-008: структурная валидация; перечня значений в docs нет |
| 6 | Регистр `content_hash` | только нижний hex | SHA-256 канонически lowercase; БД CHECK совпадает |
| 7 | Модель версий уроков | PK `(program_id, program_version, order_position)`, `id` урока повторяется между версиями | grandfathering: старые пациенты остаются на старой версии, `lesson_id` стабилен |

### S3 — JWT/RBAC (закрыт 2026-11-26)

**Цель:** каждый запрос BC#3 валидирует RS256 JWT самостоятельно, write-операции требуют `role=administrator`, revocation L1→L2 fail-closed.

Подшаги S3:

1. **S3.1 JWKS-клиент.** `GET $JWKS_URL`, кэш по `kid` TTL 1h, timeout 2s, один refetch при неизвестном `kid` не чаще раза в минуту; unknown `kid` при недоступном JWKS → 401 (fail-closed).
2. **S3.2 JWT-валидация.** RS256 only (`none`/`HS256` → 401), claims `exp`/`iat` с ±30s, `iss=urn:saymo:identity-service`, `aud=api.saymo`; `sub`, `jti`, `session_id` — непустые UUID, `role ∈ {patient, relative, administrator}`, `rev ≥ 0`.
3. **S3.3 Revocation L1→L2.** L1 in-process TTL 60s (только allow — окно stale valid по ADR-003); L2 shared `redis-blacklist` (`jwt:black:<jti>`, `account:rev:<sub>`); Redis недоступен или `account:rev` отсутствует → 401 fail-closed. L3 PostgreSQL не используется: прямые SQL-соединения между BC запрещены (`context-map.md`), нужен ADR/внутренний endpoint BC#1.
4. **S3.4 Middleware + RBAC.** `Authorization: Bearer`, 401 + `WWW-Authenticate`; `role != administrator` на write → 403; публичные GET — без JWT (контракт `endpoints.md`), patient GET урока — с JWT и ownership-проверкой через BC#4 (S4). Middleware подключён к группам роутера; сами маршруты появятся в S4/S5.
5. **S3.5 Тесты.** CSV от чистого субагента: `jwt.csv` (26), `revocation.csv` (11), `rbac.csv` (25). Тесты поднимают настоящий `httptest` JWKS с RSA-ключом, подписывают токены и проходят gin-роутер.

**Гейт S3:** `go build/vet/test -race` зелёные; `go test ./internal/auth -cover` = **76.6%**; 62 fixture-кейса; 187 PASS-записей всего. HTTP-E2E для 401/403 переносится на S5: business-маршруты BC#3 ещё не зарегистрированы, проверять нечего.

**Исправлено после ревью S3:**

- **JWKS TTL 1h реально применяется** (`jwks.go`): ключ принимается только если `now - fetchedAt < 1h`; после истечения — обязательный refetch, при недоступности JWKS — fail-closed 401. Тест `TestJWKSRefetchesAfterTTLAndRejectsRetiredKey` ротирует ключ и проверяет, что старый перестаёт приниматься.
- **Fail-closed композиция** (`router.go`): если `Auth == nil` (нет `JWKS_URL`/blacklist), защищённые группы используют `denyProtected` → 503, а не остаются без middleware. В `prod` конфиг требует `JWKS_URL` (https), `REDIS_BLACKLIST_ADDR`, `REDIS_BLACKLIST_PASSWORD`.
- 401-ответ больше не содержит топологию/URL: `{"error":"unauthorized","code":"expired|signature|..."}`; детали — в WARN-лог.
- `session_id` валидируется как UUID; `iat` обязателен; L1-кэш ограничен `min(now+60s, token.exp)`; массовый отзыв (`rev++`) также запаздывает до 60s (L1 allow) — принято по ADR-003.

### S4 — read API (реализован; E2E зелёный, ждёт ревьюера)

Реализовано:

- `internal/port/out`: port-owned DTO (`ProgramSummary`, `ProgramRecord`, `LessonRecord`) + `ProgramRepository`/`Presigner` (ADR-007).
- `internal/repository/postgres`: pgx-чтение каталога (последняя версия), карточки по версии, урока по `(program_id, lesson_id)` с выбором максимальной версии.
- `internal/usecase`: L1 5min + L2 Redis 1h + singleflight; presigned URL TTL 1h, кэш 55min; ошибки L2 не ломают чтение.
- `internal/adapter/s3`: presigned GET по постоянному `content_url` (path-style, bucket из URL).
- `internal/handler/http`: `GET /content/programs` (фильтры + 400), `GET /content/programs/:programId?version` (404/400), `GET /.../lessons/:lessonId` (JWT через auth-middleware, 401/404); публичные и auth-only группы роутера.
- `cmd/seed`: идемпотентный сид программы/уроков для E2E.

Гейт S4: `go build/vet/test -race` зелёные; `TestReadAPIFixtures` — **15/15 PASS**; coverage: handler **85.5%**, usecase **69.6%**, platform/cache **84.8%**; E2E — **7/7 PASS**, в том числе `TestLessonPresignedWithSignedToken` (RS256-токен, подписанный ключом BC#1, реальный JWKS `:8084`, shared blacklist `:6383`, presigned URL из s3mock); swagger перегенерирован (`/content/programs*` в `docs/swagger.json`). Для повторного E2E:

```powershell
$env:POSTGRES_DSN='postgres://content:content_local_pw@localhost:5435/content_db?sslmode=disable'
docker compose --env-file .env.local up -d
go run ./cmd/migrate/main.go -action up
go run ./cmd/seed/main.go
$env:E2E_SIGN_KEY='D:\Program\sayMoDev\identy-service\testdata\keys\private.pem'
$env:E2E_KID='key-v1'
go test -tags=e2e ./test/e2e/... -count=1
```

Исправлено после ревью S4:

- **Урок кэшируется L1 5min + singleflight**: L1 хранит метаданные урока (`lesson:<programId>:<lessonId>`), presign — отдельный L2-ключ; 20 параллельных запросов → 1 SQL + 1 presign (тест `TestGetLessonSingleflightAndPresignOnce`). L2-hit каталога/карточки теперь заполняет L1.
- **L1 ограничен 1024 ключами** с вытеснением (защита от уникальных фильтров); `aphasia_type` валидируется доменом (≤50, без обрамляющих пробелов); пустой фильтр = «не задан» (docs: optional), фикстура `list_07` выровнена.
- **Swagger**: аннотации `@Router` для трёх GET + `BearerAuth` security definition; `docs/` перегенерирован.
- Usecase зависит от порта `out.Cache`, а не от адаптера Redis; добавлены unit-тесты L1/L2/singleflight/фильтров и границ L1.
- 500-ошибки логируются через zap; `S3_ENDPOINT` в prod обязан быть https.
- **Presigned TTL**: в L2 хранится `{url, expiresAt}`; при остатке ≤5min ссылка перегенерируется (тест `TestPresignedURLRegeneratedWhenLessThanFiveMinutesLeft`).
- **Фильтр не размножает ключи**: полный каталог кэшируется под `programs:all`, фильтры применяются в памяти; Redis-bc3 ограничен `maxmemory 256mb` + `allkeys-lru`.

Ограничение S4 (переносится в S5): ownership «урок доступен пациенту» через BC#4 не реализован — сейчас JWT + existence, любой аутентифицированный пользователь получает урок; 403 из classification появится в S5 вместе с синхронным guard удаления.





---

## TO-BE: модель данных BC#3 (S2, из docs)

```mermaid
erDiagram
    programs ||--o{ program_lessons : "program_id"
    programs {
        uuid id PK
        text title
        text difficulty
        text aphasia_type
        int version
        int active_reference_count
        timestamptz created_at
        timestamptz updated_at
    }
    program_lessons {
        uuid id
        uuid program_id FK
        int program_version
        int order_position
        text title
        text description
        text content_url
        char64 content_hash
        bigint file_size_bytes
        text mime_type
        uuid uploaded_by
        timestamptz created_at
        timestamptz updated_at
    }
    outbox {
        uuid id PK
        text event_type
        jsonb payload
        timestamptz created_at
        timestamptz sent_at
    }
    processed_events {
        uuid event_id PK
        text event_type
        timestamptz processed_at
    }
```

Инварианты (дословно из `classifications/RehabilitationContentManagement.md`):

- `program_lessons`: PK `(program_id, program_version, order_position)`; `id` урока стабилен между версиями (grandfathering), уникален в рамках `(program_id, program_version)`.

- `programs.(difficulty, aphasia_type)` — UNIQUE; classifier неизменяем после создания.
- Программа без уроков невозможна: создание программы и уроков — одна транзакция.
- Append урока версию не меняет; вставка между уроками — инкремент `version`, уроки `K+1..N` сдвигаются, старые пациенты остаются на старой версии.
- `order_position` 1-based, без дыр; удаление урока в рамках версии не «схлопывает» нумерацию; max 200 уроков.
- `content_hash` SHA256: одинаковый файл переиспользует `content_url` (S3 не вызывается повторно).
- Удаление программы/урока блокируется, пока у BC#4 есть активные пациенты на этой версии.

### TO-BE sequence: вставка урока (S5)

```mermaid
sequenceDiagram
    autonumber
    participant A as Admin
    participant H as POST /content/programs/:id/lessons
    participant CB as S3 circuit breaker
    participant DB as PostgreSQL
    participant O as outbox
    A->>H: multipart pdf_file + insert_after_position=K
    H->>H: JWT RS256 + role=administrator
    H->>H: SHA256(pdf), проверка dedup по content_hash
    H->>CB: PutObject (только при miss, вне транзакции)
    H->>DB: BEGIN; SELECT program FOR UPDATE
    H->>DB: INSERT new version: копия уроков K+1..N со сдвигом
    H->>O: LessonInsertedIntoProgram {oldVersion,newVersion,...}
    H->>DB: COMMIT
    H-->>A: 201 {lessonId,newVersion,orderPosition,contentUrl}
```

---

## Контрактные конфликты docs (зафиксированы, решение — в шаге)

| # | Конфликт | Решение | Шаг |
|---|---|---|---|
| 1 | `classifications` §3: счётчик `active_reference_count` локально + FOR UPDATE; `endpoints.md`: guard удаления — **синхронный REST к BC#4**, «НЕ локальный счётчик» | Контракт `endpoints.md` выигрывает: перед DELETE — `GET /admin/progress/program/{id}/version/{v}/active-patients`; локальный счётчик из Kafka остаётся метрикой/индикатором, не источником истины для guard | S5 |
| 2 | `classifications` §4: «Deleting lesson: NOT supported»; `endpoints.md` + `events.md`: `DELETE .../lessons/{lessonId}` и `LessonRemovedFromProgram` | Реализуем DELETE по контракту с guard BC#4; запрет из §4 трактуем как запрет «схлопывания» нумерации, а не как отсутствие endpoint | S5 |
| 3 | Token Validation Flow требует L3 PostgreSQL (`jwt_blacklist`, `accounts.rev`), но BC#3 не владеет БД BC#1 и прямые SQL-соединения запрещены (`context-map.md`) | L1 → L2 (shared `redis-blacklist`); при недоступности L2 — fail-closed 401. Кросс-BC SQL не делаем. Требуется ADR на L3 (или BC#1 internal endpoint) | S3 |
| 4 | `deployment/*` описывает layout `core/adapter/port`, goca даёт `internal/{domain,usecase,repository,handler}` | Каталоги goca (AGENTS.md, GOCA-first), DTO на границах — в пакетах-владельцах портов (ADR-007 по смыслу: handler/port DTO не тянет домен) | S2+ |

## Инструменты и команды

`task` не установлен; прямые эквиваленты (PowerShell, из каталога сервиса):

```powershell
docker compose --env-file .env.local up -d
go run ./cmd/migrate/main.go -action up
go run ./cmd/server/main.go
go test ./... -count=1 -race
swag init -g ./cmd/server/main.go -o ./docs --parseDependency --parseInternal
```

Порты стенда: server 8003, metrics 9003, postgres **5435**, redis-bc3 **6382**, kafka 9092, s3mock 9005/9006, jaeger 16686/4317. Shared `redis-blacklist` на этой машине — **6383** (6380 занят BC#4; владелец — identy-service).
