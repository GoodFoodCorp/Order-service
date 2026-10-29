-- Transactional outbox: every lifecycle event is written with the change it
-- describes, then relayed to RabbitMQ (at-least-once) by the outbox worker.
CREATE TABLE IF NOT EXISTS outbox_events (
    id           UUID PRIMARY KEY,
    routing_key  TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    attempts     INT         NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox_events (occurred_at) WHERE published_at IS NULL;
