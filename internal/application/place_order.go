package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

type PlaceOrderInput struct {
	RestaurantID    string
	DeliveryAddress string
	Items           []domain.NewOrderItemInput
	PromoCode       string
}

// PlaceOrder creates a PLACED order for the acting customer. A promo code, if
// given, is only previewed here (never consumed) — it is redeemed for real
// once the payment actually succeeds, in ConfirmOrder.
func (uc *UseCases) PlaceOrder(ctx context.Context, actor Actor, in PlaceOrderInput) (*domain.Order, error) {
	if !actor.HasRole(RoleUser) {
		return nil, domain.NewForbiddenError("only customers can place orders")
	}
	order, err := domain.NewOrder(actor.UserID, in.RestaurantID, in.DeliveryAddress, in.Items)
	if err != nil {
		return nil, err
	}
	if in.PromoCode != "" {
		preview, err := uc.promos.Preview(ctx, actor.Token, in.PromoCode, order.TotalAmountCents)
		if err != nil {
			return nil, domain.NewValidationError(err.Error())
		}
		if err := order.ApplyPromo(preview.Code, preview.DiscountCents); err != nil {
			return nil, err
		}
	}
	if err := uc.orders.Create(ctx, order); err != nil {
		return nil, err
	}
	return order, nil
}
