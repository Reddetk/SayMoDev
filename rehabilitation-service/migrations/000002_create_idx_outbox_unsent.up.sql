BEGIN;

CREATE INDEX IF NOT EXISTS idx_outbox_unsent
    ON outbox (created_at)
    WHERE sent_at IS NULL;

COMMIT;
