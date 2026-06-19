BEGIN;

-- Append-only audit trail. Retention 3 years (ops-level policy, не в коде).
-- actor_id берётся из JWT claims (token.sub), НИКОГДА из request body.
CREATE TABLE IF NOT EXISTS audit_log (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id        UUID            NOT NULL,
    target_id       UUID            NULL,
    action          TEXT            NOT NULL,
    metadata        JSONB           NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

COMMIT;
