package domain

import (
	"testing"
	"time"
)

func TestInvoiceSplitsVATByRateAndAddsUpToWhatWasCharged(t *testing.T) {
	code := "BIENVENUE20"
	o := &Order{
		ID: "abcdef1234", TotalAmountCents: 2598, DiscountCents: 519, PromoCode: &code,
		DeliveryFeeCents: 299, TipCents: 200, Fulfillment: FulfillmentDelivery,
		Items: []OrderItem{{MenuItemName: "Burger Deluxe", Quantity: 2, UnitPriceCents: 1299}},
	}
	inv := BuildInvoice(o, &InvoiceTicket{Number: "GF-2026-000001", IssuedAt: time.Now()}, Seller{Name: "Good Food"}, "Good Food Montparnasse", "")

	if inv.TotalCents != o.AmountDueCents() || inv.TotalCents != 2598-519+299+200 {
		t.Fatalf("total = %d", inv.TotalCents)
	}
	if len(inv.VAT) != 2 || inv.VAT[0].Rate != 10 || inv.VAT[1].Rate != 20 {
		t.Fatalf("vat = %+v", inv.VAT)
	}
	// 20,79 € TTC at 10 % = 18,90 HT + 1,89 ; 2,99 € at 20 % = 2,49 HT + 0,50.
	if inv.VAT[0].BaseCents != 1890 || inv.VAT[0].VATCents != 189 || inv.VAT[1].BaseCents != 249 || inv.VAT[1].VATCents != 50 {
		t.Fatalf("vat = %+v", inv.VAT)
	}
	if inv.TotalExclVATCents+inv.TotalVATCents != inv.TotalCents {
		t.Fatal("HT + TVA must equal the total (the tip has no VAT)")
	}
	if len(inv.Lines) != 2 || inv.Lines[1].Label != "Frais de livraison" {
		t.Fatalf("lines = %+v", inv.Lines)
	}
}

func TestPickupInvoiceHasNoDeliveryLine(t *testing.T) {
	o := &Order{ID: "x", TotalAmountCents: 1000, Fulfillment: FulfillmentPickup,
		Items: []OrderItem{{MenuItemName: "Wrap", Quantity: 1, UnitPriceCents: 1000}}}
	inv := BuildInvoice(o, &InvoiceTicket{}, Seller{}, "R", "")
	if len(inv.Lines) != 1 || len(inv.VAT) != 1 || inv.TotalCents != 1000 {
		t.Fatalf("%+v", inv)
	}
}
