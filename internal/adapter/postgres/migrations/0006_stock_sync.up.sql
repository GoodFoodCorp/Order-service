ALTER TABLE orders ADD COLUMN stock_sync TEXT NOT NULL DEFAULT 'NONE';
CREATE INDEX idx_orders_stock_sync_pending ON orders (stock_sync)
    WHERE stock_sync IN ('CONSUME_PENDING', 'RESTORE_PENDING');
