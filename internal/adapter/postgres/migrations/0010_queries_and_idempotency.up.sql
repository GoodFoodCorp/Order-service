-- The franchisee portal lists a restaurant's orders, most recent first.
CREATE INDEX IF NOT EXISTS idx_orders_restaurant ON orders (restaurant_id, placed_at DESC);
-- Retried creations (same customer, same Idempotency-Key) return the first order.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_idempotency ON orders (customer_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
