package application

import (
	"context"
	"errors"

	zlog "github.com/rs/zerolog/log"

	"goodfood/order-service/internal/domain"
)

// stockUnavailableMessage is customer-facing: the underlying cause is logged,
// never returned.
const stockUnavailableMessage = "Service momentanément indisponible, veuillez réessayer dans quelques instants."

type PlaceOrderInput struct {
	RestaurantID    string
	DeliveryAddress string
	Items           []domain.NewOrderItemInput
	PromoCode       string
}

// priceUnverifiableMessage is customer-facing: the underlying cause is logged.
const priceUnverifiableMessage = "Impossible de vérifier les prix pour le moment, veuillez réessayer dans quelques instants."

// PlaceOrder creates a PLACED order for the acting customer. The prices the
// client sent are discarded: every line is priced by the menu catalog, and a
// dish that is not orderable at this restaurant refuses the whole order. Then
// the ingredients required by the dishes that have a recipe are checked
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
	if err := uc.priceFromCatalog(ctx, order); err != nil {
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

// priceFromCatalog replaces the client's prices with the catalog's. It fails
// closed: if the catalog cannot be reached the order is refused rather than
// accepted at unverified prices.
func (uc *UseCases) priceFromCatalog(ctx context.Context, order *domain.Order) error {
	priced, err := uc.catalog.PriceItems(ctx, order.RestaurantID, order.Items)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) {
			return err // e.g. "dish X is not available": meant for the customer
		}
		zlog.Warn().Err(err).Str("restaurant_id", order.RestaurantID).Msg("price lookup failed")
		return domain.NewUnavailableError(priceUnverifiableMessage)
	}
	if err := order.Reprice(priced); err != nil {
		zlog.Warn().Err(err).Str("restaurant_id", order.RestaurantID).Msg("catalog returned unusable prices")
		return domain.NewUnavailableError(priceUnverifiableMessage)
	}
	return nil
}

// checkStock refuses the order when a tracked ingredient is short. Dishes
// without a recipe are not tracked; if none is, stock-service is not called.
func (uc *UseCases) checkStock(ctx context.Context, order *domain.Order) error {
	recipes, err := uc.recipes.Lookup(ctx, order.RestaurantID, distinctMenuItemIDs(order))
	if err != nil {
		zlog.Warn().Err(err).Str("restaurant_id", order.RestaurantID).Msg("recipe lookup failed")
		return domain.NewUnavailableError(stockUnavailableMessage)
	}
	needs := domain.RequiredIngredients(order.Items, recipes)
	if len(needs) == 0 {
		return nil
	}
	availability, err := uc.stock.Check(ctx, order.RestaurantID, needs)
	if err != nil {
		zlog.Warn().Err(err).Str("restaurant_id", order.RestaurantID).Msg("stock check failed")
		return domain.NewUnavailableError(stockUnavailableMessage)
	}
	if !availability.Available {
		return domain.NewOutOfStockError(domain.ShortageMessage(order.Items, recipes, availability.Missing))
	}
	return nil
}

// distinctMenuItemIDs lists the order's dishes once each, in order.
func distinctMenuItemIDs(order *domain.Order) []string {
	seen := map[string]bool{}
	ids := make([]string, 0, len(order.Items))
	for _, item := range order.Items {
		if !seen[item.MenuItemID] {
			seen[item.MenuItemID] = true
			ids = append(ids, item.MenuItemID)
		}
	}
	return ids
}
