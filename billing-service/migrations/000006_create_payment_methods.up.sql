BEGIN;

CREATE TABLE payment_methods (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    type            TEXT NOT NULL CHECK (type IN ('card')),
    wsb_customer_id TEXT NOT NULL,
    recurring_token TEXT NOT NULL,
    card_mask       TEXT NOT NULL,
    card_exp_date   DATE,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    bound_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_payment_methods_active
    ON payment_methods (subscription_id)
    WHERE is_active;

COMMIT;