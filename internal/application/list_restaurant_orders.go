package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// ListRestaurantOrders returns every order of one restaurant, for the
// franchisee back-office. Managers are locked to their own restaurant.
func (uc *UseCases) ListRestaurantOrders(ctx context.Context, actor Actor, restaurantID string, filter domain.ListFilter) ([]domain.Order, error) {
	if !actor.HasRole(RoleManager) || actor.TenantID != restaurantID {
		return nil, domain.NewForbiddenError("you can only list orders of your own restaurant")
	}
	return uc.orders.ListByRestaurant(ctx, restaurantID, filter)
}
