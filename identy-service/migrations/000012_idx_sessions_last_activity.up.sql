BEGIN;

 Индекс для eviction ordering: oldest session by last_activity.
 ORDER BY last_activity ASC LIMIT 1 при добавлении 6-й сессии.
CREATE INDEX IF NOT EXISTS idx_sessions_last_activity
    ON sessions (account_id, last_activity ASC);

COMMIT;
