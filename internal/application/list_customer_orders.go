package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// ListCustomerOrders returns a customer's order history. Customers can only
// list their own orders; head office can list anyone's.
func (uc *UseCases) ListCustomerOrders(ctx context.Context, actor Actor, customerID string) ([]domain.Order, error) {
	if customerID == "" {
		customerID = actor.UserID
	}
	if customerID != actor.UserID && !actor.HasRole(RoleAdmin) {
		return nil, domain.NewForbiddenError("you can only list your own orders")
	}
	return uc.orders.ListByCustomer(ctx, customerID)
}
