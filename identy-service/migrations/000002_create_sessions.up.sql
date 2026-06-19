BEGIN;

CREATE TABLE IF NOT EXISTS sessions (
    session_id      UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      UUID            NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    jti             TEXT            NOT NULL,
    fingerprint     TEXT            NOT NULL,
    last_activity   TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

COMMIT;
