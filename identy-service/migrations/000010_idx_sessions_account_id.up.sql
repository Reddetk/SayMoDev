BEGIN;

-- Индекс для выборки сессий по account_id.
-- Используется при:
--   (1) проверке лимита 5 сессий на аккаунт (COUNT)
--   (2) eviction oldest last_activity (ORDER BY last_activity ASC LIMIT 1)
--   (3) mass-revoke при lock/password-change (DELETE WHERE account_id = $1)
CREATE INDEX IF NOT EXISTS idx_sessions_account_id
    ON sessions (account_id);

COMMIT;
