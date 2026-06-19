BEGIN;

-- L3 source of truth для отозванных jti.
-- L2 (Redis) является кешем. При Redis miss или недоступности -- fallback сюда.
CREATE TABLE IF NOT EXISTS jwt_blacklist (
    jti             TEXT            PRIMARY KEY,
    account_id      UUID            NOT NULL,
    expires_at      TIMESTAMPTZ     NOT NULL,
    revoked_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

COMMIT;
