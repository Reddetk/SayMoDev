BEGIN;

ALTER TABLE programs
    DROP CONSTRAINT IF EXISTS programs_difficulty_enum,
    DROP CONSTRAINT IF EXISTS programs_title_len,
    DROP CONSTRAINT IF EXISTS programs_aphasia_len;

COMMIT;
