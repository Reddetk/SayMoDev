BEGIN;

CREATE INDEX IF NOT EXISTS idx_lessons_id
    ON program_lessons (id);

COMMIT;
