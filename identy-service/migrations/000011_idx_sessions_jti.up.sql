BEGIN;

-- Уникальный индекс по jti.
-- jti уникален глобально (UUID v4). UNIQUE constraint enforced.
-- Используется при blacklist lookup и logout по jti.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_jti
    ON sessions (jti);

COMMIT;
