BEGIN;

-- pg_cron должен быть установлен в shared_preload_libraries.
-- В Docker: postgres:16-alpine не включает pg_cron по умолчанию.
-- Для локального окружения: используй образ с pg_cron или запускай cleanup вручную.
-- В prod: pg_cron устанавливается через расширение managed Postgres (Yandex Cloud).

CREATE EXTENSION IF NOT EXISTS pg_cron;

-- Удаление истёкших OTP каждые 30 минут
SELECT cron.schedule(
    'delete-expired-otps',
    '*/30 * * * *',
    $$DELETE FROM verification_codes WHERE expires_at < NOW()$$
);

-- Удаление истёкших записей jwt_blacklist раз в сутки (записи актуальны только до exp токена)
SELECT cron.schedule(
    'delete-expired-blacklist',
    '0 3 * * *',
    $$DELETE FROM jwt_blacklist WHERE expires_at < NOW()$$
);

COMMIT;
