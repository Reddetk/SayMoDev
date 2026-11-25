BEGIN;

ALTER TABLE program_lessons
    ADD CONSTRAINT program_lessons_title_len CHECK (char_length(title) <= 255),
    ADD CONSTRAINT program_lessons_description_len CHECK (char_length(description) <= 2000);

COMMIT;
