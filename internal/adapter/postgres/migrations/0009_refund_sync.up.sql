-- Refund of cancelled orders, tracked like stock so the worker can finish a
-- refund payment-service could not do at cancellation time.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS refund_sync TEXT NOT NULL DEFAULT 'NONE';
CREATE INDEX IF NOT EXISTS idx_orders_refund_pending ON orders (placed_at) WHERE refund_sync = 'PENDING';
