BEGIN;

INSERT INTO tariff_plans (id, name, week_access_bitmap, timezone) VALUES
    ('11111111-1111-1111-1111-111111111111', 'Basic',    127, 'Europe/Minsk'),
    ('22222222-2222-2222-2222-222222222222', 'Standard',  31, 'Europe/Minsk'),
    ('33333333-3333-3333-3333-333333333333', 'Premium',  127, 'Europe/Minsk')
ON CONFLICT (id) DO NOTHING;

INSERT INTO subscription_offers
    (id, tariff_plan_id, price_kopecks, currency, billing_days, valid_from, is_active)
VALUES
    ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1', '11111111-1111-1111-1111-111111111111',  2990, 'BYN', 30, '2026-01-01T00:00:00Z', true),
    ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa2', '22222222-2222-2222-2222-222222222222',  4990, 'BYN', 30, '2026-01-01T00:00:00Z', true),
    ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa3', '33333333-3333-3333-3333-333333333333',  9990, 'BYN', 30, '2026-01-01T00:00:00Z', true)
ON CONFLICT (id) DO NOTHING;

COMMIT;