BEGIN;

CREATE TABLE outbox (
    id         BIGSERIAL PRIMARY KEY,
    event_id   UUID NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    payload    JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at    TIMESTAMPTZ,
    attempts   INTEGER NOT NULL DEFAULT 0,
    last_error TEXT
);

CREATE INDEX idx_outbox_unsent ON outbox (id) WHERE sent_at IS NULL;

COMMIT;