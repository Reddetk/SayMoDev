BEGIN;

 Индекс для pg_cron cleanup job.
 DELETE FROM verification_codes WHERE expires_at < NOW() каждые 30 минут.
CREATE INDEX IF NOT EXISTS idx_verification_codes_expires_at
    ON verification_codes (expires_at)
    WHERE used = FALSE;

COMMIT;
