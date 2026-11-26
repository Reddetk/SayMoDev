BEGIN;

ALTER TABLE outbox
    ADD COLUMN IF NOT EXISTS correlation_id UUID NOT NULL DEFAULT gen_random_uuid();

COMMIT;
