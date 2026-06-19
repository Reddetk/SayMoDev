BEGIN;

CREATE TABLE IF NOT EXISTS password_history (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      UUID            NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    hash            TEXT            NOT NULL,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

COMMIT;
