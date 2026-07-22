package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// ListRestaurantOrders returns every order of one restaurant, for the
// franchisee back-office. Managers are locked to their own restaurant.
func (uc *UseCases) ListRestaurantOrders(ctx context.Context, actor Actor, restaurantID string) ([]domain.Order, error) {
	switch {
	case actor.HasRole(RoleAdmin):
	case actor.HasRole(RoleManager) && actor.TenantID == restaurantID:
	default:
		return nil, domain.NewForbiddenError("you can only list orders of your own restaurant")
	}
	return uc.orders.ListByRestaurant(ctx, restaurantID)
}
