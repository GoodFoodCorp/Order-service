package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// claimableStatuses are the order statuses a courier may be assigned to.
//
// IN_PREPARATION is deliberately included: a courier can claim an order while
// the kitchen is still cooking it and spend the travel time to the restaurant
// instead of waiting for it. The meal is then collected as soon as it is
// ready, and reaches the customer hot.
//
// Claiming does NOT advance the order: it stays IN_PREPARATION until the
// kitchen marks it READY_FOR_PICKUP, and only moves to IN_DELIVERY when the
// courier actually collects it. The two lifecycles run in parallel and meet
// at pickup — see delivery-service's pickup(), which enforces that.
var claimableStatuses = []domain.OrderStatus{
	domain.StatusInPreparation,
	domain.StatusReadyForPickup,
}

// ListReadyForDelivery returns orders awaiting a courier. Consumed by the
// delivery-service (polling, see ADR-003) and by couriers directly.
func (uc *UseCases) ListReadyForDelivery(ctx context.Context, actor Actor) ([]domain.Order, error) {
	if !actor.HasRole(RoleCourier) && !actor.HasRole(RoleAdmin) {
		return nil, domain.NewForbiddenError("only couriers can list orders ready for delivery")
	}
	return uc.orders.ListByStatuses(ctx, claimableStatuses...)
}
