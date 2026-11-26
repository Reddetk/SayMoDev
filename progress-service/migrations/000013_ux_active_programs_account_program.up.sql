BEGIN;
-- One history row per (account, program): switching back to a previously used program
-- must reactivate that row instead of inserting a duplicate.
DELETE FROM active_programs a
    USING active_programs b
    WHERE a.account_id = b.account_id
      AND a.program_id = b.program_id
      AND a.id <> b.id
      AND a.activated_at < b.activated_at;
CREATE UNIQUE INDEX IF NOT EXISTS ux_active_programs_account_program
    ON active_programs (account_id, program_id);
COMMIT;
