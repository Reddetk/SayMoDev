BEGIN;

CREATE UNIQUE INDEX IF NOT EXISTS uniq_programs_classifier
    ON programs (difficulty, aphasia_type);

COMMIT;
