BEGIN;

CREATE TABLE billing_archive (
    id          BIGSERIAL PRIMARY KEY,
    account_id  UUID NOT NULL,
    snapshot    JSONB NOT NULL,
    archived_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION billing_archive_append_only() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'billing_archive is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_billing_archive_append_only
    BEFORE UPDATE OR DELETE ON billing_archive
    FOR EACH ROW EXECUTE FUNCTION billing_archive_append_only();

COMMIT;