BEGIN;

-- PK (program_id, program_version, order_position) уже обеспечивает уникальность.
DROP INDEX IF EXISTS uniq_lessons_version_position;

COMMIT;
