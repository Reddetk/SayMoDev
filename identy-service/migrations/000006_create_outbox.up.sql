BEGIN;

CREATE TABLE IF NOT EXISTS outbox (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id    UUID            NOT NULL,
    event_type      TEXT            NOT NULL,
    payload         JSONB           NOT NULL,
    processed       BOOLEAN         NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ     NULL
);

COMMIT;
