BEGIN;

CREATE TABLE IF NOT EXISTS accounts (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT            NOT NULL,
    personal_info   JSONB           NOT NULL DEFAULT '{}',
    role            TEXT            NOT NULL CHECK (role IN ('patient', 'relative', 'administrator')),
    google_uid      TEXT            NULL,
    password_hash   TEXT            NULL,
    rev             INTEGER         NOT NULL DEFAULT 1,
    status          TEXT            NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked', 'deleted')),
    locked_until    TIMESTAMPTZ     NULL,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

COMMIT;
