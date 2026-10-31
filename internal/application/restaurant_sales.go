package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

// RestaurantSales returns a page of one restaurant's orders for the
// analytics service (internal API): amounts, statuses and dishes, which head
// office and franchisees see aggregated only.
func (uc *UseCases) RestaurantSales(ctx context.Context, restaurantID string, filter domain.ListFilter) ([]domain.Order, error) {
	if restaurantID == "" {
		return nil, domain.NewValidationError("restaurantId is required")
	}
	return uc.orders.ListByRestaurant(ctx, restaurantID, filter)
}
