BEGIN;

CREATE TABLE idempotency_keys (
    account_id    UUID NOT NULL,
    key           UUID NOT NULL,
    endpoint      TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('in_progress','completed')),
    response_code INTEGER,
    response_body JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '7 days',
    PRIMARY KEY (account_id, key)
);

CREATE INDEX idx_idempotency_expires ON idempotency_keys (expires_at);

COMMIT;