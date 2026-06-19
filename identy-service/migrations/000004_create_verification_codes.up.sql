BEGIN;

CREATE TABLE IF NOT EXISTS verification_codes (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT            NOT NULL,
    code_hash       TEXT            NOT NULL,
    purpose         TEXT            NOT NULL CHECK (purpose IN ('registration', 'password_reset', 'email_change')),
    used            BOOLEAN         NOT NULL DEFAULT FALSE,
    expires_at      TIMESTAMPTZ     NOT NULL,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

COMMIT;
