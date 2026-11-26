BEGIN;
DROP INDEX IF EXISTS ix_lesson_answers_account_lesson_submitted;
DROP INDEX IF EXISTS ix_lesson_answers_account_submitted;
DROP INDEX IF EXISTS ix_outbox_unsent;
DROP INDEX IF EXISTS ix_idempotency_keys_expires;
COMMIT;
