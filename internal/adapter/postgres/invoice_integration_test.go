//go:build integration

package postgres

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"goodfood/order-service/internal/domain"
)

func TestInvoicesAreNumberedOnceAndSentOnce(t *testing.T) {
	ctx := context.Background()
	repo := setupRepo(t)
	invoices := NewInvoiceRepository(repo.pool)

	paid := newStoredOrder(t, repo, domain.StatusConfirmed)
	now := time.Now().UTC()
	if _, err := repo.pool.Exec(ctx, `UPDATE orders SET confirmed_at = $2 WHERE id = $1`, paid.ID, now); err != nil {
		t.Fatal(err)
	}
	unpaid := newStoredOrder(t, repo, domain.StatusPlaced)
	for _, id := range []string{paid.ID, unpaid.ID} {
		if err := invoices.RecordCustomer(ctx, id, "client@example.com"); err != nil {
			t.Fatal(err)
		}
	}

	// The mail server is down once: the invoice keeps its number, waits, then goes.
	var sent []*domain.InvoiceTicket
	failing := func(context.Context, *domain.InvoiceTicket) error { return errors.New("smtp down") }
	if _, err := invoices.SendPending(ctx, 10, failing); err != nil {
		t.Fatal(err)
	}
	first, err := invoices.Issue(ctx, paid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^GF-\d{4}-\d{6}$`).MatchString(first.Number) {
		t.Fatalf("number = %q", first.Number)
	}
	if _, err := repo.pool.Exec(ctx, `UPDATE order_invoices SET last_attempt_at = now() - interval '10 minutes' WHERE order_id = $1`, paid.ID); err != nil {
		t.Fatal(err)
	}
	collect := func(_ context.Context, ticket *domain.InvoiceTicket) error { sent = append(sent, ticket); return nil }
	for i := 0; i < 2; i++ {
		if _, err := invoices.SendPending(ctx, 10, collect); err != nil {
			t.Fatal(err)
		}
	}

	mine := 0
	for _, s := range sent {
		if s.OrderID == unpaid.ID {
			t.Fatal("an unpaid order got an invoice")
		}
		if s.OrderID == paid.ID {
			mine++
			if s.Number != first.Number || s.CustomerEmail != "client@example.com" {
				t.Fatalf("sent %+v, numbered %+v", s, first)
			}
		}
	}
	if mine != 1 {
		t.Fatalf("invoice sent %d times", mine)
	}
	again, _ := invoices.Issue(ctx, paid.ID)
	if again.Number != first.Number {
		t.Fatal("an invoice keeps its number")
	}
}
