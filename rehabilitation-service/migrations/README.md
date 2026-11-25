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
| 000004 | таблица `programs` |
| 000005 | таблица `program_lessons` |
| 000006 | уникальный индекс `uniq_programs_classifier` |
| 000007 | уникальный индекс `uniq_lessons_version_position` |
| 000008 | индекс `idx_lessons_content_hash` |
| 000009 | PK `program_lessons` → `(program_id, program_version, order_position)` |
| 000010 | индекс `idx_lessons_id` |
| 000011 | CHECK-и `programs` (difficulty/title/aphasia) |
| 000012 | CHECK-и `program_lessons` (title/description) |
| 000013 | удаление дублирующего индекса `uniq_lessons_version_position` |
| 000014 | уникальный индекс `uniq_lessons_version_id` |

Доменные таблицы BC#3 (`programs`, `program_lessons`) добавлены в S2.

> `000009_...down.sql` возвращает PK `(id)` и безопасен только на БД, где `id` урока ещё не повторяется между версиями (пустая/одноверсионная). На боевых данных откат требует предварительной дедупликации — зафиксировано как ограничение down-миграции.

## Запуск

```powershell
$env:POSTGRES_DSN='postgres://content:content_local_pw@localhost:5435/content_db?sslmode=disable'
go run ./cmd/migrate/main.go -action up
go run ./cmd/migrate/main.go -action down
go run ./cmd/migrate/main.go -action force -version 3
```

`MIGRATIONS_PATH` по умолчанию `./migrations`; в образе — `/migrations`.
