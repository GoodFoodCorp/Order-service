-- The courier who claimed the delivery: only they may see the order's
-- address and move it to IN_DELIVERY / DELIVERED.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS courier_id UUID;
CREATE INDEX IF NOT EXISTS idx_orders_courier ON orders (courier_id) WHERE courier_id IS NOT NULL;
