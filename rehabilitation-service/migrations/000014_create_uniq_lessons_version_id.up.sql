BEGIN;

CREATE UNIQUE INDEX IF NOT EXISTS uniq_lessons_version_id
    ON program_lessons (program_id, program_version, id);

COMMIT;
