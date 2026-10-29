-- Click & collect, restaurant delivery fees, tips and the geocoded drop-off.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS fulfillment TEXT NOT NULL DEFAULT 'DELIVERY';
ALTER TABLE orders ADD COLUMN IF NOT EXISTS delivery_fee_cents BIGINT NOT NULL DEFAULT 0 CHECK (delivery_fee_cents >= 0);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS tip_cents BIGINT NOT NULL DEFAULT 0 CHECK (tip_cents >= 0);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS estimated_minutes INT NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS dropoff_lat DOUBLE PRECISION;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS dropoff_lng DOUBLE PRECISION;
