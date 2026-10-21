DROP INDEX IF EXISTS idx_orders_stock_sync_pending;
ALTER TABLE orders DROP COLUMN stock_sync;
