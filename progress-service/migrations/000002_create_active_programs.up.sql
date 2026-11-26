BEGIN;
CREATE TABLE IF NOT EXISTS active_programs (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         uuid        NOT NULL,
    program_id         uuid        NOT NULL,
    version            integer     NOT NULL,
    status             text        NOT NULL CHECK (status IN ('active', 'paused', 'completed')),
    difficulty         text        NOT NULL,
    aphasia_type       text        NOT NULL DEFAULT '',
    activated_at       timestamptz NOT NULL DEFAULT now(),
    completed_at       timestamptz,
    paused_at          timestamptz,
    completion_percent integer     NOT NULL DEFAULT 0 CHECK (completion_percent BETWEEN 0 AND 100)
);
COMMIT;