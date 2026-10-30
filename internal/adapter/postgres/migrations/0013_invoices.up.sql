-- One invoice per paid order: who it goes to, its number and whether it was
-- sent. Numbers come from a sequence, so they follow each other and are never
-- given twice (the year is part of the number).
CREATE SEQUENCE IF NOT EXISTS invoice_number_seq;

CREATE TABLE IF NOT EXISTS order_invoices (
    order_id       UUID PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
    customer_email TEXT        NOT NULL DEFAULT '',
    invoice_number TEXT        UNIQUE,
    issued_at      TIMESTAMPTZ,
    sent_at        TIMESTAMPTZ,
    attempts       INT         NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The invoice worker only ever looks at invoices still to send.
CREATE INDEX IF NOT EXISTS order_invoices_to_send_idx ON order_invoices (created_at)
    WHERE sent_at IS NULL AND customer_email <> '';
