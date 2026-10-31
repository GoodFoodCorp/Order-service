package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"goodfood/order-service/internal/domain"
)

type fakeInvoices struct{ recorded map[string]string }

func (f *fakeInvoices) RecordCustomer(_ context.Context, orderID, email string) error {
	f.recorded[orderID] = email
	return nil
}

func (f *fakeInvoices) Issue(_ context.Context, orderID string) (*domain.InvoiceTicket, error) {
	return &domain.InvoiceTicket{OrderID: orderID, Number: "GF-2026-000001", IssuedAt: time.Now(), CustomerEmail: f.recorded[orderID]}, nil
}

func (f *fakeInvoices) SendPending(context.Context, int, func(context.Context, *domain.InvoiceTicket) error) (int, error) {
	return 0, nil
}

func TestInvoiceIsForTheCustomerAndTheRestaurantOnly(t *testing.T) {
	uc, orders, _, _ := setup()
	uc.WithInvoices(InvoiceDeps{Repo: &fakeInvoices{recorded: map[string]string{}}, Seller: domain.Seller{Name: "Good Food"}})
	now := time.Now()
	courierID := courier.UserID
	paid := &domain.Order{ID: "o-paid", CustomerID: customer.UserID, RestaurantID: "resto-1", Status: domain.StatusDelivered,
		ConfirmedAt: &now, CourierID: &courierID, TotalAmountCents: 1000,
		Items: []domain.OrderItem{{MenuItemName: "Burger", Quantity: 1, UnitPriceCents: 1000}}}
	unpaid := &domain.Order{ID: "o-unpaid", CustomerID: customer.UserID, RestaurantID: "resto-1", Status: domain.StatusPlaced}
	orders.orders[paid.ID], orders.orders[unpaid.ID] = paid, unpaid

	for _, who := range []Actor{customer, manager} {
		inv, err := uc.GetInvoice(context.Background(), who, paid.ID)
		if err != nil || inv.Number != "GF-2026-000001" || inv.TotalCents != 1000 {
			t.Fatalf("%s: %+v, %v", who.UserID, inv, err)
		}
	}
	var derr *domain.Error
	for _, who := range []Actor{courier, admin} {
		if _, err := uc.GetInvoice(context.Background(), who, paid.ID); !errors.As(err, &derr) || derr.Code != domain.ErrCodeForbidden {
			t.Fatalf("%s must not see the customer's invoice: %v", who.UserID, err)
		}
	}
	if _, err := uc.GetInvoice(context.Background(), customer, unpaid.ID); !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("no invoice before payment: %v", err)
	}
}
