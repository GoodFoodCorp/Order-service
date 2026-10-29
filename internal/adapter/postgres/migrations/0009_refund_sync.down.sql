DROP INDEX IF EXISTS idx_orders_refund_pending;
ALTER TABLE orders DROP COLUMN IF EXISTS refund_sync;
