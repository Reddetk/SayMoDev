BEGIN;
CREATE TABLE IF NOT EXISTS outbox (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id    text        NOT NULL UNIQUE,
    event_type  text        NOT NULL,
    payload     jsonb       NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    sent_at     timestamptz
);
COMMIT;