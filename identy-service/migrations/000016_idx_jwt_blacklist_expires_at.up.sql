BEGIN;

 Индекс для очистки просроченных записей blacklist (L3).
 DELETE FROM jwt_blacklist WHERE expires_at < NOW()
 Записи в blacklist актуальны только пока токен не истёк.
CREATE INDEX IF NOT EXISTS idx_jwt_blacklist_expires_at
    ON jwt_blacklist (expires_at);

COMMIT;
