BEGIN;

CREATE TABLE IF NOT EXISTS program_lessons (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    program_id      UUID NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    program_version INTEGER NOT NULL CHECK (program_version >= 1),
    order_position  INTEGER NOT NULL CHECK (order_position BETWEEN 1 AND 200),
    title           TEXT NOT NULL CHECK (length(btrim(title)) > 0),
    description     TEXT NOT NULL DEFAULT '',
    content_url     TEXT NOT NULL CHECK (length(btrim(content_url)) > 0),
    content_hash    TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    file_size_bytes BIGINT NOT NULL CHECK (file_size_bytes > 0),
    mime_type       TEXT NOT NULL DEFAULT 'application/pdf' CHECK (mime_type = 'application/pdf'),
    uploaded_by     UUID NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
