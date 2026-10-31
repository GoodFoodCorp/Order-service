package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// ListCustomerOrders returns a customer's order history, to that customer
// only.
func (uc *UseCases) ListCustomerOrders(ctx context.Context, actor Actor, customerID string, filter domain.ListFilter) ([]domain.Order, error) {
	if customerID == "" {
		customerID = actor.UserID
	}
	if customerID != actor.UserID {
		return nil, domain.NewForbiddenError("you can only list your own orders")
	}
	return uc.orders.ListByCustomer(ctx, customerID, filter)
}
