BEGIN;

CREATE TABLE account_status (
    account_id UUID PRIMARY KEY,
    status     TEXT NOT NULL CHECK (status IN ('active','locked','deleted')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;