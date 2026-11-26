BEGIN;

-- payments не удаляются при удалении аккаунта (ADR-005): финансовый audit trail.
CREATE TABLE payments (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id       UUID NOT NULL,
    amount_kopecks        BIGINT NOT NULL CHECK (amount_kopecks BETWEEN -100000000 AND 100000000 AND amount_kopecks <> 0),
    currency              TEXT NOT NULL CHECK (currency IN ('BYN','RUB','USD','EUR')),
    status                TEXT NOT NULL CHECK (status IN ('pending','success','failed','refunded')),
    payment_kind          TEXT NOT NULL CHECK (payment_kind IN ('initial','renewal','upgrade_proration')),
    tariff_plan_name      TEXT NOT NULL,
    external_reference_id TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payments_subscription ON payments (subscription_id, created_at DESC);
CREATE UNIQUE INDEX uq_payments_external_ref ON payments (external_reference_id)
    WHERE external_reference_id IS NOT NULL;

CREATE TRIGGER trg_payments_updated_at
    BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;