package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

type PlaceOrderInput struct {
	RestaurantID    string
	DeliveryAddress string
	Items           []domain.NewOrderItemInput
}

// PlaceOrder creates a PLACED order for the acting customer.
func (uc *UseCases) PlaceOrder(ctx context.Context, actor Actor, in PlaceOrderInput) (*domain.Order, error) {
	if !actor.HasRole(RoleUser) {
		return nil, domain.NewForbiddenError("only customers can place orders")
	}
	order, err := domain.NewOrder(actor.UserID, in.RestaurantID, in.DeliveryAddress, in.Items)
	if err != nil {
		return nil, err
	}
	if err := uc.orders.Create(ctx, order); err != nil {
		return nil, err
	}
	return order, nil
}
