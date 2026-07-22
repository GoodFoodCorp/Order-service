CREATE TABLE IF NOT EXISTS orders (
    id                 UUID PRIMARY KEY,
    customer_id        UUID        NOT NULL,
    restaurant_id      UUID        NOT NULL,
    status             TEXT        NOT NULL,
    total_amount_cents BIGINT      NOT NULL CHECK (total_amount_cents >= 0),
    delivery_address   TEXT        NOT NULL,
    placed_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders (customer_id, placed_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_status   ON orders (status);

CREATE TABLE IF NOT EXISTS order_items (
    id                   UUID PRIMARY KEY,
    order_id             UUID   NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    menu_item_id         UUID   NOT NULL,
    menu_item_name       TEXT   NOT NULL,
    quantity             INT    NOT NULL CHECK (quantity > 0),
    unit_price_cents     BIGINT NOT NULL CHECK (unit_price_cents > 0),
    special_instructions TEXT   NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items (order_id);

CREATE TABLE IF NOT EXISTS payments (
    id               UUID PRIMARY KEY,
    order_id         UUID        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    stripe_intent_id TEXT        NOT NULL,
    status           TEXT        NOT NULL,
    amount_cents     BIGINT      NOT NULL,
    currency         TEXT        NOT NULL,
    paid_at          TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_order ON payments (order_id);
