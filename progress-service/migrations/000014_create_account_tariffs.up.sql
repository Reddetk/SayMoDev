BEGIN;
CREATE TABLE IF NOT EXISTS account_tariffs (
    account_id         uuid        PRIMARY KEY,
    week_bitmap        integer     NOT NULL CHECK (week_bitmap BETWEEN 0 AND 127),
    timezone           text        NOT NULL,
    valid_until        timestamptz,
    activated_at       timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL DEFAULT now()
);
COMMIT;
