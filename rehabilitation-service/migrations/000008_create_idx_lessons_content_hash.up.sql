BEGIN;

CREATE INDEX IF NOT EXISTS idx_lessons_content_hash
    ON program_lessons (content_hash);

COMMIT;
