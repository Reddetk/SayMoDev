# Миграции БД (BC#3)

Источник правил — `Reddetk/saymo-documentation/deployment/migrations.md`.

## Соглашения

- Имя: `NNNNNN_<snake_case>.up.sql` / `.down.sql`, номер — 6 цифр, монотонно возрастающий.
- Одна таблица или один индекс — один номер.
- Каждый файл транзакционен: `BEGIN; ... COMMIT;`.
- `down` обязателен для каждого `up`; удаление через `DROP ... IF EXISTS`.
- Уже применённую миграцию не редактировать — только новый номер.

## Текущие миграции

| Номер | Объект |
|---|---|
| 000001 | таблица `outbox` |
| 000002 | индекс `idx_outbox_unsent` |
| 000003 | таблица `processed_events` |

Доменные таблицы BC#3 (`programs`, `program_lessons`) добавляются в S2.

## Запуск

```powershell
$env:POSTGRES_DSN='postgres://content:content_local_pw@localhost:5435/content_db?sslmode=disable'
go run ./cmd/migrate/main.go -action up
go run ./cmd/migrate/main.go -action down
go run ./cmd/migrate/main.go -action force -version 3
```

`MIGRATIONS_PATH` по умолчанию `./migrations`; в образе — `/migrations`.
