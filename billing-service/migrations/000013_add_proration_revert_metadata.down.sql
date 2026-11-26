BEGIN;

ALTER TABLE payments
    DROP COLUMN IF EXISTS applied_offer_id,
    DROP COLUMN IF EXISTS revert_tariff_plan_id,
    DROP COLUMN IF EXISTS revert_offer_id,
    DROP COLUMN IF EXISTS revert_week_bitmap,
    DROP COLUMN IF EXISTS revert_timezone;

COMMIT;