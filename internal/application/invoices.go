package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// InvoiceDeps are what invoices need: the numbering, the mail server, the
// restaurants' names and the seller printed on every invoice.
type InvoiceDeps struct {
	Repo        domain.InvoiceRepository
	Mailer      domain.InvoiceMailer // nil: invoices are not emailed (no SMTP)
	Restaurants domain.RestaurantDirectory
	Seller      domain.Seller
}

// WithInvoices turns invoices on: recorded at checkout, emailed once paid,
// downloadable from the order page.
func (uc *UseCases) WithInvoices(d InvoiceDeps) *UseCases {
	uc.invoices = &d
	return uc
}

// recordInvoiceCustomer remembers where the order's invoice goes. Best
// effort: the order is placed even if this fails (the invoice can still be
// downloaded).
func (uc *UseCases) recordInvoiceCustomer(ctx context.Context, actor Actor, order *domain.Order) {
	if uc.invoices == nil || actor.Email == "" {
		return
	}
	_ = uc.invoices.Repo.RecordCustomer(ctx, order.ID, actor.Email)
}

// GetInvoice returns the invoice of a paid order to its customer, the
// restaurant and head office. It is numbered on first request if the worker
// has not done it yet.
func (uc *UseCases) GetInvoice(ctx context.Context, actor Actor, orderID string) (*domain.Invoice, error) {
	if uc.invoices == nil {
		return nil, domain.NewNotFoundError("invoices are not available")
	}
	order, err := uc.GetOrder(ctx, actor, orderID)
	if err != nil {
		return nil, err
	}
	// The invoice names the customer: not for the courier who delivered it.
	if !order.IsOwnedBy(actor.UserID) && !actor.HasRole(RoleAdmin) && !(actor.HasRole(RoleManager) && actor.TenantID == order.RestaurantID) {
		return nil, domain.NewForbiddenError("only the customer, the restaurant and head office see the invoice")
	}
	if order.ConfirmedAt == nil {
		return nil, domain.NewNotFoundError("no invoice: this order has not been paid")
	}
	if order.IsOwnedBy(actor.UserID) {
		uc.recordInvoiceCustomer(ctx, actor, order)
	}
	ticket, err := uc.invoices.Repo.Issue(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	return uc.buildInvoice(ctx, order, ticket), nil
}

// SendPendingInvoices emails the invoices of paid orders not sent yet; it
// returns how many were handled.
func (uc *UseCases) SendPendingInvoices(ctx context.Context, limit int) (int, error) {
	if uc.invoices == nil || uc.invoices.Mailer == nil {
		return 0, nil
	}
	return uc.invoices.Repo.SendPending(ctx, limit, func(ctx context.Context, t *domain.InvoiceTicket) error {
		order, err := uc.orders.GetByID(ctx, t.OrderID)
		if err != nil {
			return err
		}
		return uc.invoices.Mailer.SendInvoice(ctx, uc.buildInvoice(ctx, order, t))
	})
}

func (uc *UseCases) buildInvoice(ctx context.Context, order *domain.Order, t *domain.InvoiceTicket) *domain.Invoice {
	name, address := "Restaurant Good Food", ""
	if uc.invoices.Restaurants != nil {
		if n, a, err := uc.invoices.Restaurants.Describe(ctx, order.RestaurantID); err == nil && n != "" {
			name, address = n, a
		}
	}
	return domain.BuildInvoice(order, t, uc.invoices.Seller, name, address)
}
