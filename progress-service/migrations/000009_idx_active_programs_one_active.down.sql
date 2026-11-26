BEGIN;
DROP INDEX IF EXISTS ux_active_programs_one_active;
DROP INDEX IF EXISTS ix_active_programs_account_history;
COMMIT;
