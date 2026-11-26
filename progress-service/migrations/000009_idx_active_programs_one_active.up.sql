BEGIN;
CREATE UNIQUE INDEX IF NOT EXISTS ux_active_programs_one_active
    ON active_programs (account_id) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS ix_active_programs_account_history
    ON active_programs (account_id, activated_at DESC);
COMMIT;