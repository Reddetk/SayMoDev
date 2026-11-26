BEGIN;

CREATE TABLE tariff_plans (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name               TEXT NOT NULL UNIQUE,
    week_access_bitmap SMALLINT NOT NULL CHECK (week_access_bitmap BETWEEN 0 AND 127),
    timezone           TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;