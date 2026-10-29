package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// maxClaimableListed bounds the orders awaiting a courier returned at once.
//
// A courier can claim an order while the kitchen is still cooking it
// (IN_PREPARATION) and spend the travel time to the restaurant instead of
// waiting; claiming does not advance the order, which only moves to
// IN_DELIVERY at pickup (see delivery-service). Pickups (click & collect) are
// never offered to couriers.
const maxClaimableListed = 500

// ListReadyForDelivery returns orders awaiting a courier, to head office.
// Couriers see them through delivery-service, which exposes only what a
// courier needs; delivery-service itself uses the internal API.
func (uc *UseCases) ListReadyForDelivery(ctx context.Context, actor Actor) ([]domain.Order, error) {
	if !actor.HasRole(RoleAdmin) {
		return nil, domain.NewForbiddenError("only head office can list every order awaiting a courier")
	}
	return uc.orders.ListAwaitingCourier(ctx, maxClaimableListed)
}
