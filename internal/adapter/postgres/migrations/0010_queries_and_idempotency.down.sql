DROP INDEX IF EXISTS idx_orders_idempotency;
ALTER TABLE orders DROP COLUMN IF EXISTS idempotency_key;
DROP INDEX IF EXISTS idx_orders_restaurant;
