 000021_pg_cron_cleanup.up.sql

 pg_cron недоступен в postgres:16-alpine (локальное окружение).
 В prod (Yandex Cloud managed Postgres) pg_cron включается через консоль провайдера.

 Cleanup верификационных кодов и jwt_blacklist выполняется application-слоем:
   - периодическая горутина в identity-service (interval: 30m / 3h).

 Миграция оставлена пустой для сохранения ссылочной целостности нумерации.
SELECT 1;
