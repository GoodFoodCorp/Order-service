DROP INDEX IF EXISTS idx_orders_restaurant_cancellation;
ALTER TABLE orders DROP COLUMN IF EXISTS cancelled_at;
ALTER TABLE orders DROP COLUMN IF EXISTS cancellation_comment;
ALTER TABLE orders DROP COLUMN IF EXISTS cancellation_reason;
ALTER TABLE orders DROP COLUMN IF EXISTS cancelled_by;
