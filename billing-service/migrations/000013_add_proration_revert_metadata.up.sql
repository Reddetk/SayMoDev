BEGIN;

-- Метаданные для компенсации upgrade_proration, если webhook не пришёл.
ALTER TABLE payments
    ADD COLUMN applied_offer_id      UUID,
    ADD COLUMN revert_tariff_plan_id UUID,
    ADD COLUMN revert_offer_id       UUID,
    ADD COLUMN revert_week_bitmap    SMALLINT CHECK (revert_week_bitmap BETWEEN 0 AND 127),
    ADD COLUMN revert_timezone       TEXT;

COMMIT;