BEGIN;

-- Индекс для async outbox processor.
-- SELECT * FROM outbox WHERE processed = FALSE ORDER BY created_at ASC LIMIT N
-- Partial index только по unprocessed записям -- обработанные не сканируются.
CREATE INDEX IF NOT EXISTS idx_outbox_pending
    ON outbox (created_at ASC)
    WHERE processed = FALSE;

COMMIT;
