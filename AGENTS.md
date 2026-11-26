# AGENTS.md — sayMoDev

Монорепозиторий SayMo (PWA реабилитации после инсульта). Цель: **полностью реализовать продукт из документации силами агентов и GOCA**. Сервис BC#1 в репозитории — прототип «для презентации»; новое поведение берётся только из документированных контрактов.

## Источники истины (порядок при конфликте)

1. `Reddetk/saymo-documentation` — **private** (github MCP / `gh` под аккаунтом Reddetk; анонимный fetch даёт 404):
   `BC.md`, `context-map.md`, `classifications/<BC>.md`, `endpoints.md`, `events.md`, `EventDataFlow.md`, `glossary.md`, `Observability.md`, `adr/*`, `deployment/*` (taskfile, testing, migrations, docker).
2. `<service>/README.md` — локальная копия спецификации BC + заметки. Может отставать от docs.
3. Код.

Поля, коды ошибок, имена событий (`PascalCase` noun-verb), лимиты — копировать из docs дословно, не «улучшать». ADR обязательны: например ADR-007 требует, чтобы DTO принадлежали пакету порта (`port/in`, `port/out`), а не `core/valObj` / `core/entity`.

## Границы монорепо

| каталог | BC | состояние |
|---|---|---|
| `identy-service` | BC#1 IAM | прототип: гексагон `internal/{core,port,adapter}`, **не** goca-layout |
| `billing-stub` / `content-service` / `progress-service` | BC#2/3/4 | ещё не созданы |
| `notif` (BC#5) | — | запланирован |

- `identy-service` — именно так (историческая опечатка). Module path и ссылки в docs завязаны на это имя: не переименовывать.
- Один `go.mod` на сервис, `go.work` нет: команды запускать из каталога сервиса, кросс-сервисных импортов не делать.
- Пути из docs (`identy-service/core/...`, `port/...`) устарели — фактически всё под `internal/`.

## Обязательный пайплайн (нарушение = работа не принята)

0. **Один агент — один микросервис.** Чужие сервисы не трогать.
1. **План** сервиса — в `<service>/README.md`: шаги со ссылками на docs/ADR, у каждого шага цель и **AS-IS → TO-BE в цифрах** (эндпоинты, лимиты, задержки, покрытие). Диаграммы (`diagram-design`) — классы/сущности и sequence — прикладывать к README: это AS-IS, на него ссылаются шаги.
2. **Декомпозиция по одному шагу.** Раскладывать на подшаги только текущий шаг плана; следующий — после того как текущий закрыт. Первый шаг всегда: окружение (goca + docker compose), тестовый стенд со всеми зависимостями, минимальный health-check каждой среды.
3. **Четыре гейта перед первой строкой кода** — явно в ответе: (а) проговорить, что делаешь; (б) найти подходящий скил (skill / интернет); (в) изложить последовательность действий; (г) аудит по **SOLID, KISS, ООП, DDD**.
4. **TDD.** Фикстуры приходят от чистого субагента **без контекста**: он пишет CSV «как человек» + border cases типов Go (пустое, max/min, unicode, пароль >72 байт, NULL-поля) и обязан задействовать ветки **3xx/4xx/5xx**. Сначала красные тесты из этих CSV, потом код.
5. **Гейты приёмки шага:** субагент-ревьюер (скил `golang-code-review` + сверка с docs) и субагент-E2E, который знает **только пункт плана и контракты из `endpoints.md`** и ходит исключительно через HTTP API сервиса. Пока E2E не зелёный, шаг не закрыт.
6. **GOCA-first.** Сначала `goca` (`init`, `feature`, `entity`, `usecase`, `repository`, `handler`, `di`, `integrate`, `mocks`, `interfaces`, `middleware`, `test-integration`); если генератор не покрывает — читать его шаблоны/`goca doctor|analyze`, и только потом писать руками с маркером `// goca:manual — <причина>`, чтобы перегенерация не стёрла.

Definition of done шага: `go vet ./...`, `go build ./...`, `go test ./... -count=1 -race` зелёные, E2E зелёный, `docs/` (swagger) перегенерирован, план в README сдвинут, миграции парные (`NNNNNN_*.up.sql` / `.down.sql`).

## Новый сервис: goca

```bash
goca init <service> --module github.com/Reddetk/SayMoDev/<service> --template enterprise
```

`--template enterprise` — базовый выбор (JWT+OAuth2, RBAC, Redis, Prometheus, OTel, swagger, Docker). Внимание: goca генерирует `internal/{domain,usecase,repository,handler}` + GORM + `net/http`. BC#1 использует gin + pgx + порты/адаптеры и **не** переводится на goca-layout без отдельного ADR — для него goca применяется точечно (новые сущности/сервисы), расхождение фиксируется в README плана.

## Команды (проверено на этой машине)

Работать в `identy-service/`. `task` и `golangci-lint` **не установлены** (`go install github.com/go-task/task/v3/cmd/task@latest` при необходимости), поэтому прямые эквиваленты:

```bash
go run ./cmd/genrsa/main.go                 # task gen-rsa, RSA-ключи в testdata/keys
docker compose --env-file .env.local up -d postgres redis-bc1 redis-blacklist
go run ./cmd/migrate/main.go -action up     # task migrate:up
go run ./cmd/main.go                        # требует env (см. ниже)
go run ./cmd/postboxmock/main.go            # OTP-инбокс http://localhost:9025
go test ./... -count=1 -race
swag init -g ./cmd/main.go -o ./docs --parseDependency --parseInternal
```

Порты (`.env.local`): server 8080, postgres **5433**, redis 6379, redis-blacklist 6380, jaeger 16686/4317. `cmd/main.go` **не** читает `.env` — `config.Load()` падает на отсутствующих переменных, поэтому без `task` сначала экспортировать переменные из `.env.local` в окружение.

## Ловушки

- **`go test ./...` сейчас красный** (baseline, не ваша регрессия): `TestInitiateGoogleOAuth_HappyPath` сравнивает `valobj.OAuthState` с `in.OAuthState` — разные именованные типы, поля совпадают. Это следствие ADR-007; чинить сравнением DTO-полей, а не откатом портов. `go vet` и `go build` чистые.
- `task docker:up` содержит `docker compose up build -d` (опечатка) → вручную `docker compose --env-file .env.local up --build -d`.
- `docker compose` сам `.env.local` не читает (ищет `.env`), а `.env` в репозитории нет.
- Эндпоинтов `/health`, `/ready`, `/metrics` нет вообще — если шаг плана требует health-check, его надо создать, а не искать.
- `docs/` (swagger) закоммичен: после правки handler'ов/аннотаций перегенерировать, иначе контракт в репозитории врёт.
- Миграции — golang-migrate, 6-значный префикс, обязательная пара up/down; `000021` включает `pg_cron`-очистку OTP.
- Моки — один пакет `mocks/` для портов; конфигурации mockery в репозитории нет, менять моки вместе с интерфейсом.
- Тестов мало: один файл `internal/core/auth_service_test.go`; фикстуры — `testdata/fixtures.go` (`Restore*`-конструкторы обходят инварианты, время всегда относительное `time.Now()`).
- Windows/PowerShell: файлы UTF-8 с кириллицей. `Get-Content`/`Out-File` без `-Encoding utf8` портят текст — читать через read-инструмент, писать через write/edit.
- Секреты: `.env*`, `*.pem`, `local_jwks.json` не коммитить; `AGENTS.md` раньше был в `.gitignore` — теперь отслеживается.
