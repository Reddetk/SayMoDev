BEGIN;

-- Урок одной и той же программы существует в нескольких версиях (grandfathering):
-- id урока повторяется, поэтому PK — составной по версии и позиции.
ALTER TABLE program_lessons DROP CONSTRAINT IF EXISTS program_lessons_pkey;
ALTER TABLE program_lessons ADD CONSTRAINT program_lessons_pkey
    PRIMARY KEY (program_id, program_version, order_position);

COMMIT;
