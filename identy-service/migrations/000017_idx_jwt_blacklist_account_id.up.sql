BEGIN;

-- Индекс для выборки всех jti по account_id при mass-revoke.
-- Используется при lock/password-change: SELECT jti FROM jwt_blacklist WHERE account_id = $1
-- для сбора jti перед записью в Redis.
CREATE INDEX IF NOT EXISTS idx_jwt_blacklist_account_id
    ON jwt_blacklist (account_id);

COMMIT;
