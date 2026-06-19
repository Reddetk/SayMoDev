BEGIN;

-- Индекс для выборки audit-событий по target_id (аккаунт которого касается действие).
-- Используется в admin-панели: GET /admin/audit?target=<uuid>
CREATE INDEX IF NOT EXISTS idx_audit_log_target_id
    ON audit_log (target_id, created_at DESC)
    WHERE target_id IS NOT NULL;

COMMIT;
