package application

import (
	"context"
	"time"

	"github.com/google/uuid"
	zlog "github.com/rs/zerolog/log"

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
// couriers only on the delivery-related transitions.
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
	default:
		return nil, domain.NewForbiddenError("you are not allowed to update order status")
	}

	if err := order.TransitionTo(target); err != nil {
		return nil, err
	}
	if err := uc.orders.UpdateStatus(ctx, order); err != nil {
		return nil, err
	}
	if target == domain.StatusReadyForPickup {
		uc.publishOrderReady(ctx, order)
	}
	return order, nil
}

// publishOrderReady is best-effort: the status change is already committed,
// and delivery-service's REST sync recovers any lost event.
func (uc *UseCases) publishOrderReady(ctx context.Context, order *domain.Order) {
	event := domain.OrderReadyEvent{
		EventID:         uuid.NewString(),
		OrderID:         order.ID,
		CustomerID:      order.CustomerID,
		RestaurantID:    order.RestaurantID,
		DeliveryAddress: order.DeliveryAddress,
		OccurredAt:      time.Now().UTC(),
	}
	if err := uc.events.PublishOrderReady(ctx, event); err != nil {
		zlog.Warn().Err(err).Str("order_id", order.ID).Msg("could not publish order.ready_for_pickup")
	}
}
