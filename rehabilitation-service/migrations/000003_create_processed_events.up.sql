BEGIN;

CREATE TABLE IF NOT EXISTS processed_events (
    event_id     UUID PRIMARY KEY,
    event_type   TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
