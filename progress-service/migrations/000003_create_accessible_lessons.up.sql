BEGIN;
CREATE TABLE IF NOT EXISTS accessible_lessons (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id     uuid        NOT NULL,
    lesson_id      uuid        NOT NULL,
    program_id     uuid        NOT NULL,
    lesson_type    text        NOT NULL CHECK (lesson_type IN ('regular', 'creative')),
    order_position integer     NOT NULL CHECK (order_position >= 1),
    granted_at     timestamptz NOT NULL DEFAULT now(),
    revoked_at     timestamptz
);
COMMIT;