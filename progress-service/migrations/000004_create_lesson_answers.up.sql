BEGIN;
CREATE TABLE IF NOT EXISTS lesson_answers (
    answer_id    uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   uuid        NOT NULL,
    lesson_id    uuid        NOT NULL,
    program_id   uuid        NOT NULL,
    media_type   text        NOT NULL CHECK (media_type IN ('text', 'photo', 'video')),
    payload      text        NOT NULL,
    submitted_at timestamptz NOT NULL DEFAULT now(),
    anonymised   boolean     NOT NULL DEFAULT false
);
COMMIT;