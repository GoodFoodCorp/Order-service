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

// PlaceOrder creates a PLACED order for the acting customer. Before anything
// else, the ingredients required by the dishes that have a recipe are checked
// against stock-service (read only: quantities are consumed on confirmation).
// A promo code, if
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
	if err := uc.checkStock(ctx, order); err != nil {
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

// checkStock refuses the order when a tracked ingredient is short. Dishes
// without a recipe are not tracked; if none is, stock-service is not called.
func (uc *UseCases) checkStock(ctx context.Context, order *domain.Order) error {
	seen := map[string]bool{}
	menuItemIDs := make([]string, 0, len(order.Items))
	for _, item := range order.Items {
		if !seen[item.MenuItemID] {
			seen[item.MenuItemID] = true
			menuItemIDs = append(menuItemIDs, item.MenuItemID)
		}
	}
	recipes, err := uc.recipes.Lookup(ctx, order.RestaurantID, menuItemIDs)
	if err != nil {
		return domain.NewUnavailableError("recipe lookup failed: " + err.Error())
	}
	needs := domain.RequiredIngredients(order.Items, recipes)
	if len(needs) == 0 {
		return nil
	}
	availability, err := uc.stock.Check(ctx, order.RestaurantID, needs)
	if err != nil {
		return domain.NewUnavailableError("stock check failed: " + err.Error())
	}
	if !availability.Available {
		return domain.NewOutOfStockError(domain.ShortageMessage(order.Items, recipes, availability.Missing))
	}
	return nil
}
