BEGIN;

ALTER TABLE program_lessons
    DROP CONSTRAINT IF EXISTS program_lessons_title_len,
    DROP CONSTRAINT IF EXISTS program_lessons_description_len;

COMMIT;
