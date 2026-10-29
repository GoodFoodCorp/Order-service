package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// The use cases below back the internal API used by delivery-service, which
// owns deliveries and couriers. They trust the courier id it sends: the
// service key authenticates delivery-service itself.

// AssignCourier records the courier who claimed the order's delivery. It is
// idempotent for the same courier and refused once another courier holds it.
func (uc *UseCases) AssignCourier(ctx context.Context, orderID, courierID string) (*domain.Order, error) {
	if courierID == "" {
		return nil, domain.NewValidationError("courier id is required")
	}
	order, err := uc.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.IsAssignedTo(courierID) {
		return order, nil
	}
	if !order.IsClaimableByCourier() {
		return nil, domain.NewConflictError("order cannot be claimed in status " + string(order.Status))
	}
	applied, err := uc.orders.AssignCourier(ctx, orderID, courierID)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, domain.NewConflictError("order already claimed by another courier")
	}
	order.CourierID = &courierID
	return order, nil
}

// UnassignCourier frees an order a courier gave up before pickup.
func (uc *UseCases) UnassignCourier(ctx context.Context, orderID, courierID string) error {
	return uc.orders.UnassignCourier(ctx, orderID, courierID)
}

// UpdateStatusForCourier applies a delivery transition on behalf of a courier
// (pickup, drop-off), with exactly the rules of a courier calling directly.
func (uc *UseCases) UpdateStatusForCourier(ctx context.Context, orderID, courierID string, target domain.OrderStatus) (*domain.Order, error) {
	courier := Actor{UserID: courierID, RoleSlugs: []string{RoleCourier}}
	return uc.UpdateOrderStatus(ctx, courier, orderID, target)
}

// GetOrderForDelivery returns an order to delivery-service.
func (uc *UseCases) GetOrderForDelivery(ctx context.Context, orderID string) (*domain.Order, error) {
	return uc.orders.GetByID(ctx, orderID)
}

// ListClaimableOrders returns the orders awaiting a courier, for
// delivery-service's reconciliation.
func (uc *UseCases) ListClaimableOrders(ctx context.Context) ([]domain.Order, error) {
	return uc.orders.ListAwaitingCourier(ctx, maxClaimableListed)
}
