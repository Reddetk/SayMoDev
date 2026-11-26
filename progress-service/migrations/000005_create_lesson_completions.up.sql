BEGIN;
CREATE TABLE IF NOT EXISTS lesson_completions (
    account_id   uuid        NOT NULL,
    lesson_id    uuid        NOT NULL,
    program_id   uuid        NOT NULL,
    completed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, lesson_id)
);
COMMIT;