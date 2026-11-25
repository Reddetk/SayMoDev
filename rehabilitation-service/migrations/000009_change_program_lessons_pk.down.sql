BEGIN;

ALTER TABLE program_lessons DROP CONSTRAINT IF EXISTS program_lessons_pkey;
ALTER TABLE program_lessons ADD CONSTRAINT program_lessons_pkey PRIMARY KEY (id);

COMMIT;
