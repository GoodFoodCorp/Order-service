package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"goodfood/order-service/internal/domain"
)

// maxInvoiceAttempts: an invoice the mail server keeps refusing stops being
// retried (it can still be downloaded from the order page).
const maxInvoiceAttempts = 5

type InvoiceRepository struct {
	pool *pgxpool.Pool
}

func NewInvoiceRepository(pool *pgxpool.Pool) *InvoiceRepository {
	return &InvoiceRepository{pool: pool}
}

func (r *InvoiceRepository) RecordCustomer(ctx context.Context, orderID, email string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO order_invoices (order_id, customer_email) VALUES ($1, $2)
		 ON CONFLICT (order_id) DO UPDATE SET customer_email = EXCLUDED.customer_email
		 WHERE order_invoices.customer_email = ''`, orderID, email)
	return err
}

// issue gives the invoice its number if it has none (COALESCE only draws
// from the sequence when needed).
const issueSQL = `UPDATE order_invoices
	SET invoice_number = COALESCE(invoice_number, 'GF-' || to_char(now(), 'YYYY') || '-' || lpad(nextval('invoice_number_seq')::text, 6, '0')),
	    issued_at = COALESCE(issued_at, now())
	WHERE order_id = $1
	RETURNING order_id, invoice_number, issued_at, customer_email`

func scanTicket(row pgx.Row) (*domain.InvoiceTicket, error) {
	var t domain.InvoiceTicket
	if err := row.Scan(&t.OrderID, &t.Number, &t.IssuedAt, &t.CustomerEmail); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *InvoiceRepository) Issue(ctx context.Context, orderID string) (*domain.InvoiceTicket, error) {
	// Orders placed before invoices existed have no row yet.
	if _, err := r.pool.Exec(ctx, `INSERT INTO order_invoices (order_id) VALUES ($1) ON CONFLICT DO NOTHING`, orderID); err != nil {
		return nil, err
	}
	return scanTicket(r.pool.QueryRow(ctx, issueSQL, orderID))
}

func (r *InvoiceRepository) SendPending(ctx context.Context, limit int, send func(context.Context, *domain.InvoiceTicket) error) (int, error) {
	sent := 0
	for i := 0; i < limit; i++ {
		done, err := r.sendOne(ctx, send)
		if err != nil {
			return sent, err
		}
		if !done {
			return sent, nil
		}
		sent++
	}
	return sent, nil
}

// sendOne locks the oldest invoice to send (another replica skips it), sends
// it and records the outcome in the same transaction. done is false when
// nothing is left to send.
func (r *InvoiceRepository) sendOne(ctx context.Context, send func(context.Context, *domain.InvoiceTicket) error) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var orderID string
	err = tx.QueryRow(ctx,
		`SELECT i.order_id FROM order_invoices i JOIN orders o ON o.id = i.order_id
		 WHERE i.sent_at IS NULL AND i.customer_email <> '' AND i.attempts < $1
		   AND (i.last_attempt_at IS NULL OR i.last_attempt_at < now() - interval '5 minutes')
		   AND o.confirmed_at IS NOT NULL
		 ORDER BY i.created_at LIMIT 1
		 FOR UPDATE OF i SKIP LOCKED`, maxInvoiceAttempts).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ticket, err := scanTicket(tx.QueryRow(ctx, issueSQL, orderID))
	if err != nil {
		return false, fmt.Errorf("number invoice of %s: %w", orderID, err)
	}
	if sendErr := send(ctx, ticket); sendErr != nil {
		if _, err := tx.Exec(ctx, `UPDATE order_invoices SET attempts = attempts + 1, last_attempt_at = now() WHERE order_id = $1`, orderID); err != nil {
			return false, err
		}
		// The number stays: a retried invoice keeps the one it was given.
		return true, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE order_invoices SET sent_at = now() WHERE order_id = $1`, orderID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
