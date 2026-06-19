BEGIN;
SELECT cron.unschedule('delete-expired-otps');
SELECT cron.unschedule('delete-expired-blacklist');
DROP EXTENSION IF EXISTS pg_cron;
COMMIT;
