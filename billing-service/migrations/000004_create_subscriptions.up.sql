BEGIN;

CREATE TABLE subscriptions (
    id                            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id                    UUID NOT NULL,
    offer_id                      UUID NOT NULL REFERENCES subscription_offers(id) ON DELETE RESTRICT,
    tariff_plan_id                UUID NOT NULL REFERENCES tariff_plans(id) ON DELETE RESTRICT,
    status                        TEXT NOT NULL CHECK (status IN ('pending','active','cancelled','expired','suspended')),
    week_bitmap                   SMALLINT NOT NULL CHECK (week_bitmap BETWEEN 0 AND 127),
    timezone                      TEXT NOT NULL,
    period_start                  TIMESTAMPTZ NOT NULL,
    period_end                    TIMESTAMPTZ NOT NULL,
    scheduled_target_tariff_plan  UUID REFERENCES tariff_plans(id) ON DELETE RESTRICT,
    scheduled_effective_at        TIMESTAMPTZ,
    revoked_at                    TIMESTAMPTZ,
    retry_count                   INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    next_retry_at                 TIMESTAMPTZ,
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (period_end > period_start),
    CHECK (scheduled_target_tariff_plan IS NULL OR scheduled_effective_at IS NOT NULL)
);

-- Инвариант §1: максимум одна неистёкшая подписка на аккаунт.
CREATE UNIQUE INDEX uq_subscriptions_account_alive
    ON subscriptions (account_id)
    WHERE status IN ('pending','active','suspended');

CREATE INDEX idx_subscriptions_due ON subscriptions (status, period_end);
CREATE INDEX idx_subscriptions_active_tariff ON subscriptions (account_id) WHERE status = 'active';

CREATE TRIGGER trg_subscriptions_updated_at
    BEFORE UPDATE ON subscriptions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;