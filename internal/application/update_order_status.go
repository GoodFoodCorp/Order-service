package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// courierAllowedTargets lists the only transitions a courier may trigger
// (accepting a delivery, completing it). Everything else is back-office.
var courierAllowedTargets = map[domain.OrderStatus]bool{
	domain.StatusInDelivery: true,
	domain.StatusDelivered:  true,
}

// UpdateOrderStatus applies a back-office lifecycle change.
// Managers act on their own restaurant only; head office on any order;
// couriers only on the delivery-related transitions of the orders assigned to
// them.
func (uc *UseCases) UpdateOrderStatus(ctx context.Context, actor Actor, orderID string, target domain.OrderStatus) (*domain.Order, error) {
	order, err := uc.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	switch {
	case actor.HasRole(RoleAdmin):
	case actor.HasRole(RoleManager):
		if actor.TenantID != order.RestaurantID {
			return nil, domain.NewForbiddenError("managers can only update orders of their own restaurant")
		}
	case actor.HasRole(RoleCourier):
		if !courierAllowedTargets[target] {
			return nil, domain.NewForbiddenError("couriers can only set delivery-related statuses")
		}
		if !order.IsAssignedTo(actor.UserID) {
			return nil, domain.NewForbiddenError("this delivery is assigned to another courier")
		}
	default:
		return nil, domain.NewForbiddenError("you are not allowed to update order status")
	}

	previous := order.Status
	if err := order.TransitionTo(target); err != nil {
		return nil, err
	}
	// The event goes to the outbox with the change itself: a status change is
	// never committed without its event, nor an event sent for a rolled-back
	// change.
	if target == domain.StatusCancelled {
		return uc.cancel(ctx, order)
	}
	if err := uc.orders.UpdateStatus(ctx, order, previous, domain.NewOrderEvent(order)); err != nil {
		return nil, err
	}
	return order, nil
}
