BEGIN;

DROP INDEX IF EXISTS uq_verification_codes_email_purpose;

 Восстанавливаем partial unique index из 000022.
CREATE UNIQUE INDEX IF NOT EXISTS uq_verification_codes_email_purpose_active
    ON verification_codes (email, purpose)
    WHERE used = FALSE;

COMMIT;
