BEGIN;
CREATE TABLE IF NOT EXISTS patient_progress (
    account_id           uuid        NOT NULL,
    program_id           uuid        NOT NULL,
    program_version      integer     NOT NULL,
    status               text        NOT NULL CHECK (status IN ('active', 'paused', 'completed')),
    completed_lessons    integer     NOT NULL DEFAULT 0 CHECK (completed_lessons >= 0),
    total_lessons        integer     NOT NULL CHECK (total_lessons > 0),
    completion_percent   integer     NOT NULL DEFAULT 0 CHECK (completion_percent BETWEEN 0 AND 100),
    week_bitmap          integer     CHECK (week_bitmap BETWEEN 0 AND 127),
    timezone             text,
    tariff_valid_until   timestamptz,
    tariff_activated_at  timestamptz,
    last_completed_at    timestamptz,
    last_updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, program_id)
);
COMMIT;