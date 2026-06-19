BEGIN;

-- Индекс для выборки последних 5 хешей паролей по account_id.
-- ORDER BY created_at DESC LIMIT 5 при проверке reuse и trim до 5 записей.
CREATE INDEX IF NOT EXISTS idx_password_history_account_id
    ON password_history (account_id, created_at DESC);

COMMIT;
