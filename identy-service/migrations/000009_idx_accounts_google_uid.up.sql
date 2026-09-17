BEGIN;

 Unique partial index по google_uid.
 NULL не нарушает уникальность в PostgreSQL (NULL != NULL),
 поэтому partial WHERE google_uid IS NOT NULL явно документирует намерение.
CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_google_uid
    ON accounts (google_uid)
    WHERE google_uid IS NOT NULL;

COMMIT;
