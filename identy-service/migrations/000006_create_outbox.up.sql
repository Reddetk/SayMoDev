BEGIN;

-- Transactional outbox для Kafka-событий.
-- Запись в outbox выполняется в той же ACID-транзакции что и бизнес-операция.
-- Async-процессор читает pending записи и публикует в Kafka, затем помечает processed=true.
CREATE TABLE IF NOT EXISTS outbox (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id    UUID            NOT NULL,
    aggregate_type  TEXT            NOT NULL,
    event_type      TEXT            NOT NULL,
    payload         JSONB           NOT NULL,
    processed       BOOLEAN         NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ     NULL
);

COMMIT;
