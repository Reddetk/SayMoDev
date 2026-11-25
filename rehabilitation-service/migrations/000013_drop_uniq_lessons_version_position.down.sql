BEGIN;

CREATE UNIQUE INDEX IF NOT EXISTS uniq_lessons_version_position
    ON program_lessons (program_id, program_version, order_position);

COMMIT;
