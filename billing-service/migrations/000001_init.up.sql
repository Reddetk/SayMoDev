BEGIN;

-- UUID-генерация для всех таблиц BC#2 (gen_random_uuid).
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Единый триггер updated_at: время всегда пишет БД (инвариант §11 Temporal).
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMIT;
