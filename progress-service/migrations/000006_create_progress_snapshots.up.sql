BEGIN;
CREATE TABLE IF NOT EXISTS progress_snapshots (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         uuid        NOT NULL,
    program_id         uuid        NOT NULL,
    completion_percent integer     NOT NULL CHECK (completion_percent BETWEEN 0 AND 100),
    snapshot_reason    text        NOT NULL CHECK (snapshot_reason IN ('program_completed', 'difficulty_changed', 'tariff_changed')),
    created_at         timestamptz NOT NULL DEFAULT now()
);
COMMIT;