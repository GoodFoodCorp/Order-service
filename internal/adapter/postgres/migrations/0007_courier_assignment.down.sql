DROP INDEX IF EXISTS idx_orders_courier;
ALTER TABLE orders DROP COLUMN IF EXISTS courier_id;
