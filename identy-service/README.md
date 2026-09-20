# SayMo Identity Service

Identity and Access Management (BC#1) для платформы SayMo — PWA для помощи людям после инсульта.

Сервис отвечает за регистрацию, аутентификацию, управление сессиями и токенами доступа. Полная доменная спецификация: [saymo-documentation](https://github.com/Reddetk/saymo-documentation).

## Технологии

| Категория | Технология |
| --- | --- |
| Язык | Go |
| HTTP framework | Gin |
| База данных | PostgreSQL |
| Кэш / rate limiting / token blacklist | Redis |
| Асинхронные события | Kafka (Outbox pattern) |
| Аутентификация | JWT (RS256), JWKS, OAuth2 (Google) |
| Хеширование паролей | bcrypt |
| Наблюдаемость | OpenTelemetry, Jaeger, структурированные логи (Zap) |
| API-документация | Swagger (swaggo/gin-swagger) |
| Контейнеризация | Docker, docker-compose |
| Task runner | Taskfile (go-task) |

## Архитектура

Сервис построен по принципам Hexagonal / Clean Architecture:

- `internal/core` — доменная бизнес-логика (AuthService, AccountService, SessionService, TokenService, OTPService)
- `internal/adapter/primary/http` — входящие HTTP-адаптеры (handlers, middleware: JWT, observability, CORS)
- `internal/adapter/secondary` — исходящие адаптеры (PostgreSQL, Redis, Kafka outbox)
- `migrations` — SQL-миграции базы данных
- `mocks` — сгенерированные моки для тестов

## Быстрый старт

Требуется Docker и docker-compose.

\`\`\`bash
cp .env.example .env
docker-compose up -d
\`\`\`

Если установлен [Task](https://taskfile.dev):

\`\`\`bash
task --list
\`\`\`

Swagger UI после запуска доступен по адресу `/swagger/index.html`.

## Документация домена

documentation — [Reddetk/saymo-documentation](https://github.com/Reddetk/saymo-documentation).
