package domain

import (
	"context"
	"time"
)

// French VAT on a meal delivered or taken away (restauration, 10 %) and on
// the delivery service (20 %). A tip is not subject to VAT.
const (
	FoodVATRate     = 10
	DeliveryVATRate = 20
)

// Seller is who issues the invoices: Good Food's head office.
type Seller struct {
	Name      string
	Address   string
	SIRET     string
	VATNumber string
}

type InvoiceLine struct {
	Label          string
	Quantity       int
	UnitPriceCents int64
	TotalCents     int64
	VATRate        int
}

// VATLine is the VAT of one rate, on the amounts after discount.
type VATLine struct {
	Rate      int
	BaseCents int64
	VATCents  int64
}

// Invoice of a paid order. Amounts include VAT unless named otherwise.
type Invoice struct {
	Number            string
	IssuedAt          time.Time
	OrderID           string
	CustomerEmail     string
	Seller            Seller
	RestaurantName    string
	RestaurantAddress string
	Fulfillment       Fulfillment
	DeliveryAddress   string
	Lines             []InvoiceLine
	PromoCode         string
	DiscountCents     int64
	TipCents          int64
	VAT               []VATLine
	TotalExclVATCents int64
	TotalVATCents     int64
	// TotalCents is what was charged: the order's amount due.
	TotalCents int64
}

// InvoiceTicket is an issued invoice number, before it is sent.
type InvoiceTicket struct {
	OrderID       string
	Number        string
	IssuedAt      time.Time
	CustomerEmail string
}

// InvoiceRepository keeps who gets each order's invoice, its number (from a
// sequence: numbers follow each other, never reused) and whether it was sent.
type InvoiceRepository interface {
	// RecordCustomer remembers the email the invoice goes to (once).
	RecordCustomer(ctx context.Context, orderID, email string) error
	// Issue returns the order's invoice number, giving it one first.
	Issue(ctx context.Context, orderID string) (*InvoiceTicket, error)
	// SendPending numbers and sends invoices of confirmed orders not sent
	// yet, at most limit: each is locked while send runs (one replica sends
	// it), marked sent when send succeeds, retried later otherwise.
	SendPending(ctx context.Context, limit int, send func(context.Context, *InvoiceTicket) error) (int, error)
}

// RestaurantDirectory names a restaurant on its invoices.
type RestaurantDirectory interface {
	Describe(ctx context.Context, restaurantID string) (name, address string, err error)
}

// InvoiceMailer sends an invoice to its customer.
type InvoiceMailer interface {
	SendInvoice(ctx context.Context, invoice *Invoice) error
}

// BuildInvoice computes the invoice of a paid order.
func BuildInvoice(o *Order, t *InvoiceTicket, seller Seller, restaurantName, restaurantAddress string) *Invoice {
	inv := &Invoice{
		Number: t.Number, IssuedAt: t.IssuedAt, OrderID: o.ID, CustomerEmail: t.CustomerEmail,
		Seller: seller, RestaurantName: restaurantName, RestaurantAddress: restaurantAddress,
		Fulfillment: o.Fulfillment, DeliveryAddress: o.DeliveryAddress,
		DiscountCents: o.DiscountCents, TipCents: o.TipCents,
	}
	if o.PromoCode != nil {
		inv.PromoCode = *o.PromoCode
	}
	for _, it := range o.Items {
		inv.Lines = append(inv.Lines, InvoiceLine{
			Label: it.MenuItemName, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents,
			TotalCents: it.UnitPriceCents * int64(it.Quantity), VATRate: FoodVATRate,
		})
	}
	if o.DeliveryFeeCents > 0 {
		inv.Lines = append(inv.Lines, InvoiceLine{
			Label: "Frais de livraison", Quantity: 1, UnitPriceCents: o.DeliveryFeeCents,
			TotalCents: o.DeliveryFeeCents, VATRate: DeliveryVATRate,
		})
	}
	food := o.TotalAmountCents - o.DiscountCents
	if food < 0 {
		food = 0
	}
	for _, part := range []struct {
		rate  int
		gross int64
	}{{FoodVATRate, food}, {DeliveryVATRate, o.DeliveryFeeCents}} {
		if part.gross == 0 {
			continue
		}
		base := excludingVAT(part.gross, part.rate)
		inv.VAT = append(inv.VAT, VATLine{Rate: part.rate, BaseCents: base, VATCents: part.gross - base})
		inv.TotalExclVATCents += base
		inv.TotalVATCents += part.gross - base
	}
	inv.TotalExclVATCents += o.TipCents
	inv.TotalCents = o.AmountDueCents()
	return inv
}

// excludingVAT is the amount before VAT, rounded to the nearest cent.
func excludingVAT(gross int64, rate int) int64 {
	return (gross*100*2 + int64(100+rate)) / (int64(100+rate) * 2)
}
