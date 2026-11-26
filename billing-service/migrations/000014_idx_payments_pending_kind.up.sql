BEGIN;

-- Ускоряет поиск stale pending-платежей (renewal/proration) в воркере.
CREATE INDEX idx_payments_pending_kind_created
    ON payments (payment_kind, created_at)
    WHERE status = 'pending';

COMMIT;
