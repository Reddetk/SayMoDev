BEGIN;

CREATE TABLE IF NOT EXISTS programs (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title                  TEXT NOT NULL CHECK (length(btrim(title)) > 0),
    difficulty             TEXT NOT NULL CHECK (length(btrim(difficulty)) > 0),
    aphasia_type           TEXT NOT NULL CHECK (length(btrim(aphasia_type)) > 0),
    version                INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    active_reference_count INTEGER NOT NULL DEFAULT 0 CHECK (active_reference_count >= 0),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
