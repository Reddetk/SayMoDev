BEGIN;

 DROP обычного индекса из 000014, он не подходит для ON CONFLICT.
DROP INDEX IF EXISTS idx_verification_codes_email_purpose;

 UNIQUE partial index: одна активная запись (email, purpose) в любой момент времени.
 WHERE used = FALSE соответствует условию фильтрации в адаптере Upsert.
 Postgres поддерживает partial unique index в ON CONFLICT начиная с версии 9.5.
CREATE UNIQUE INDEX IF NOT EXISTS uq_verification_codes_email_purpose_active
    ON verification_codes (email, purpose)
    WHERE used = FALSE;

COMMIT;
