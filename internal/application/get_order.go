package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// GetOrder returns one order, restricted to its owner, the restaurant's
// manager (tenant match), head office, or the courier assigned to it — never
// to any courier: the order holds the customer's address.
func (uc *UseCases) GetOrder(ctx context.Context, actor Actor, orderID string) (*domain.Order, error) {
	order, err := uc.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	switch {
	case order.IsOwnedBy(actor.UserID):
	case actor.HasRole(RoleAdmin):
	case actor.HasRole(RoleCourier) && order.IsAssignedTo(actor.UserID):
	case actor.HasRole(RoleManager) && actor.TenantID == order.RestaurantID:
	default:
		return nil, domain.NewForbiddenError("you are not allowed to view this order")
	}
	return order, nil
}
