BEGIN;

ALTER TABLE programs
    ADD CONSTRAINT programs_difficulty_enum CHECK (difficulty IN ('easy', 'medium', 'hard')),
    ADD CONSTRAINT programs_title_len CHECK (char_length(title) <= 255),
    ADD CONSTRAINT programs_aphasia_len CHECK (char_length(aphasia_type) <= 50);

COMMIT;
