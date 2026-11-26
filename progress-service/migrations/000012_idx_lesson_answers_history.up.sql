BEGIN;
CREATE INDEX IF NOT EXISTS ix_lesson_answers_account_lesson_submitted
    ON lesson_answers (account_id, lesson_id, submitted_at DESC);
CREATE INDEX IF NOT EXISTS ix_lesson_answers_account_submitted
    ON lesson_answers (account_id, submitted_at DESC);
CREATE INDEX IF NOT EXISTS ix_outbox_unsent
    ON outbox (occurred_at) WHERE sent_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_idempotency_keys_expires
    ON idempotency_keys (expires_at);
COMMIT;