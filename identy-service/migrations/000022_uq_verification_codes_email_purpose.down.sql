BEGIN;

DROP INDEX IF EXISTS uq_verification_codes_email_purpose_active;

-- Восстанавливаем обычный индекс из 000014.
CREATE INDEX IF NOT EXISTS idx_verification_codes_email_purpose
    ON verification_codes (email, purpose)
    WHERE used = FALSE;

COMMIT;
