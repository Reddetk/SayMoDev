BEGIN;
CREATE TABLE IF NOT EXISTS idempotency_keys (
    event_id   text        PRIMARY KEY,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
COMMIT;