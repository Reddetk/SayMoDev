BEGIN;

-- Уникальный индекс по email.
-- Инвариант BC#1: email глобально уникален.
-- Partial index по status != 'deleted' не применяется:
-- удалённый email должен блокировать повторную регистрацию (soft delete, не физическое удаление).
CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_email
    ON accounts (email);

COMMIT;
