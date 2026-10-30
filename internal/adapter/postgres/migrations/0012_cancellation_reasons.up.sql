-- Who cancelled an order, why, and when (reason codes: domain/cancellation.go).
ALTER TABLE orders ADD COLUMN IF NOT EXISTS cancelled_by TEXT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS cancellation_reason TEXT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS cancellation_comment TEXT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS cancelled_at TIMESTAMPTZ;
-- Cancellation statistics per restaurant (analytics, back-office).
CREATE INDEX IF NOT EXISTS idx_orders_restaurant_cancellation
    ON orders (restaurant_id, cancellation_reason) WHERE cancellation_reason IS NOT NULL;
