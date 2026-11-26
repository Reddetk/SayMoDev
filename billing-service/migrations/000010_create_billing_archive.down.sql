BEGIN;
DROP TABLE IF EXISTS billing_archive;
DROP FUNCTION IF EXISTS billing_archive_append_only();
COMMIT;