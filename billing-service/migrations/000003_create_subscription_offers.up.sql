BEGIN;

CREATE TABLE subscription_offers (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tariff_plan_id UUID NOT NULL REFERENCES tariff_plans(id) ON DELETE RESTRICT,
    price_kopecks  BIGINT NOT NULL CHECK (price_kopecks BETWEEN 100 AND 100000000),
    currency       TEXT NOT NULL CHECK (currency IN ('BYN','RUB','USD','EUR')),
    billing_days   INTEGER NOT NULL CHECK (billing_days BETWEEN 1 AND 365),
    valid_from     TIMESTAMPTZ NOT NULL,
    valid_until    TIMESTAMPTZ,
    is_active      BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (valid_until IS NULL OR valid_until > valid_from)
);

CREATE INDEX idx_subscription_offers_plan ON subscription_offers (tariff_plan_id, is_active);

COMMIT;