BEGIN;
CREATE UNIQUE INDEX IF NOT EXISTS ux_accessible_lessons_one_granted
    ON accessible_lessons (account_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_accessible_lessons_account
    ON accessible_lessons (account_id, granted_at DESC);
COMMIT;