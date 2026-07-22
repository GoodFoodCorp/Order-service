package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// ListReadyForDelivery returns orders awaiting a courier. Consumed by the
// delivery-service (polling, see ADR-003) and by couriers directly.
func (uc *UseCases) ListReadyForDelivery(ctx context.Context, actor Actor) ([]domain.Order, error) {
	if !actor.HasRole(RoleCourier) && !actor.HasRole(RoleAdmin) {
		return nil, domain.NewForbiddenError("only couriers can list orders ready for delivery")
	}
	return uc.orders.ListByStatus(ctx, domain.StatusReadyForPickup)
}
