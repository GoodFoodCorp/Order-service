package invoicedoc

import (
	"strings"
	"testing"
	"time"

	"goodfood/order-service/internal/domain"
)

func TestEurosTheFrenchWay(t *testing.T) {
	for cents, want := range map[int64]string{2378: "23,78 €", 123450: "1 234,50 €", 5: "0,05 €"} {
		if got := Euros(cents); got != want {
			t.Errorf("%d: %q, want %q", cents, got, want)
		}
	}
}

func TestInvoicePageEscapesWhatCustomersAndRestaurantsTyped(t *testing.T) {
	inv := &domain.Invoice{Number: "GF-2026-000001", IssuedAt: time.Now(), OrderID: "abcdef1234",
		RestaurantName: "<script>alert(1)</script>", Lines: []domain.InvoiceLine{{Label: "Burger", Quantity: 1, TotalCents: 1000, VATRate: 10}}}
	page, err := HTML(inv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "<script>alert") || !strings.Contains(string(page), "GF-2026-000001") {
		t.Fatal("the page must escape data and show the number")
	}
}
