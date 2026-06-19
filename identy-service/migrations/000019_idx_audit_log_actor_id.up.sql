BEGIN;

-- Индекс для выборки audit-событий по actor_id.
-- Используется в admin-панели: GET /admin/audit?actor=<uuid>
CREATE INDEX IF NOT EXISTS idx_audit_log_actor_id
    ON audit_log (actor_id, created_at DESC);

COMMIT;
