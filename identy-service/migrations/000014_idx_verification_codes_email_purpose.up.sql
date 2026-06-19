BEGIN;

-- Индекс для lookup активного (не использованного, не истёкшего) OTP по email + purpose.
-- Используется при:
--   (1) регистрации: WHERE email=$1 AND purpose='registration' AND used=FALSE AND expires_at > NOW()
--   (2) password reset: purpose='password_reset'
--   (3) email change: purpose='email_change'
CREATE INDEX IF NOT EXISTS idx_verification_codes_email_purpose
    ON verification_codes (email, purpose)
    WHERE used = FALSE;

COMMIT;
