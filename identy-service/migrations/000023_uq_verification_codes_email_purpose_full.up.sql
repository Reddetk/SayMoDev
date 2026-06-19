BEGIN;

-- Убираем partial unique index из 000022 -- он не подходит для ON CONFLICT без WHERE.
DROP INDEX IF EXISTS uq_verification_codes_email_purpose_active;

-- Полный UNIQUE index без предиката.
-- ON CONFLICT (email, purpose) в адаптере теперь найдёт его без изменений в SQL.
CREATE UNIQUE INDEX IF NOT EXISTS uq_verification_codes_email_purpose
    ON verification_codes (email, purpose);

COMMIT;
