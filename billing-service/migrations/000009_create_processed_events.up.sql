BEGIN;

CREATE TABLE processed_events (
    event_id     UUID NOT NULL,
    consumer     TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, consumer)
);

COMMIT;