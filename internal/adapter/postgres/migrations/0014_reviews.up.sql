-- Verified reviews: one per received order, left by its customer.
CREATE TABLE IF NOT EXISTS order_reviews (
    order_id      UUID PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
    restaurant_id UUID        NOT NULL,
    customer_id   UUID        NOT NULL,
    rating        SMALLINT    NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment       TEXT        NOT NULL DEFAULT '',
    ordered_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A restaurant's page lists its reviews newest first, and its mark is
-- computed from them.
CREATE INDEX IF NOT EXISTS order_reviews_restaurant_idx ON order_reviews (restaurant_id, created_at DESC);
