package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goodfood/order-service/internal/domain"
)

// Regression: order-service used to trust the unit price sent by the browser
// ("Prices come from the client for this POC"): a Burger Deluxe (12.99 €) could
// be ordered for 1 cent. The catalog (menu-service) is now the price authority.

const (
	burgerID = "11111111-1111-4111-8111-111111111111"
	sodaID   = "22222222-2222-4222-8222-222222222222"
)

// strictCatalog prices from a fixed menu and refuses anything else.
type strictCatalog struct {
	menu  map[string]domain.OrderItem // canonical id -> authoritative name + price
	err   error
	calls []string // restaurant ids asked
}

func (c *strictCatalog) PriceItems(_ context.Context, restaurantID string, items []domain.OrderItem) ([]domain.OrderItem, error) {
	c.calls = append(c.calls, restaurantID)
	if c.err != nil {
		return nil, c.err
	}
	out := make([]domain.OrderItem, 0, len(items))
	for _, it := range items {
		dish, ok := c.menu[it.MenuItemID]
		if !ok {
			return nil, domain.NewValidationError("« " + it.MenuItemName + " » n'est pas disponible dans ce restaurant")
		}
		it.MenuItemName, it.UnitPriceCents = dish.MenuItemName, dish.UnitPriceCents
		out = append(out, it)
	}
	return out, nil
}

func realMenu() map[string]domain.OrderItem {
	return map[string]domain.OrderItem{
		burgerID: {MenuItemName: "Burger Deluxe", UnitPriceCents: 1299},
		sodaID:   {MenuItemName: "Soda", UnitPriceCents: 300},
	}
}

func setupPriced(catalog domain.MenuCatalog) (*UseCases, *fakeOrderRepo, *fakePromos, *fakeStock) {
	orders := newFakeOrderRepo()
	promos := newFakePromos()
	stock := newFakeStock()
	return NewUseCases(orders, newFakePayments(), promos, domain.NoopPublisher{}, newFakeRecipes(), stock, catalog), orders, promos, stock
}

func placeWith(uc *UseCases, promo string, items ...domain.NewOrderItemInput) (*domain.Order, error) {
	return uc.PlaceOrder(context.Background(), customer, PlaceOrderInput{
		RestaurantID: "resto-1", DeliveryAddress: "12 rue de Paris", Items: items, PromoCode: promo,
	})
}

func TestPlaceOrderIgnoresThePriceTheClientSends(t *testing.T) {
	uc, orders, _, _ := setupPriced(&strictCatalog{menu: realMenu()})

	order, err := placeWith(uc, "", domain.NewOrderItemInput{MenuItemID: burgerID, MenuItemName: "Burger Deluxe", Quantity: 1, UnitPriceCents: 1})

	require.NoError(t, err)
	assert.Equal(t, int64(1299), order.Items[0].UnitPriceCents, "the catalog price, not 1 cent")
	assert.Equal(t, int64(1299), order.TotalAmountCents)
	require.Len(t, orders.orders, 1)
	for _, saved := range orders.orders {
		assert.Equal(t, int64(1299), saved.TotalAmountCents, "what is persisted — and later charged — is the real price")
	}
}

func TestPlaceOrderUsesTheCatalogNameToo(t *testing.T) {
	uc, _, _, _ := setupPriced(&strictCatalog{menu: realMenu()})

	order, err := placeWith(uc, "", domain.NewOrderItemInput{MenuItemID: burgerID, MenuItemName: "Gratuit offert", Quantity: 2, UnitPriceCents: 1299})

	require.NoError(t, err)
	assert.Equal(t, "Burger Deluxe", order.Items[0].MenuItemName)
	assert.Equal(t, int64(2*1299), order.TotalAmountCents)
}

func TestPlaceOrderSumsSeveralDishesAtCatalogPrices(t *testing.T) {
	uc, _, _, _ := setupPriced(&strictCatalog{menu: realMenu()})

	order, err := placeWith(uc, "",
		domain.NewOrderItemInput{MenuItemID: burgerID, MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 5},
		domain.NewOrderItemInput{MenuItemID: sodaID, MenuItemName: "Soda", Quantity: 3, UnitPriceCents: 5},
	)

	require.NoError(t, err)
	assert.Equal(t, int64(2*1299+3*300), order.TotalAmountCents)
}

func TestPlaceOrderRefusesADishThatIsNotOnTheMenu(t *testing.T) {
	uc, orders, _, stock := setupPriced(&strictCatalog{menu: realMenu()})

	_, err := placeWith(uc, "", domain.NewOrderItemInput{
		MenuItemID: "99999999-9999-4999-8999-999999999999", MenuItemName: "Plat inventé", Quantity: 1, UnitPriceCents: 1,
	})

	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCodeValidation, de.Code)
	assert.Empty(t, orders.orders, "no order is created")
	assert.Zero(t, stock.checkCalls, "and stock is not even consulted")
}

func TestPlaceOrderFailsClosedWhenThePricesCannotBeVerified(t *testing.T) {
	uc, orders, _, _ := setupPriced(&strictCatalog{err: errors.New("menu-service unreachable")})

	_, err := placeWith(uc, "", domain.NewOrderItemInput{MenuItemID: burgerID, MenuItemName: "Burger", Quantity: 1, UnitPriceCents: 1299})

	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCodeUnavailable, de.Code, "never accept unverified prices")
	assert.Empty(t, orders.orders)
}

func TestPlaceOrderKeepsClassifiedCatalogErrors(t *testing.T) {
	// A validation error from the catalog (dish unavailable) must reach the
	// customer as is, not be flattened into "service unavailable".
	uc, _, _, _ := setupPriced(&strictCatalog{err: domain.NewValidationError("« Burger » n'est plus disponible")})

	_, err := placeWith(uc, "", domain.NewOrderItemInput{MenuItemID: burgerID, MenuItemName: "Burger", Quantity: 1, UnitPriceCents: 1299})

	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCodeValidation, de.Code)
	assert.Contains(t, de.Message, "n'est plus disponible")
}

func TestPlaceOrderPromoIsPreviewedOnTheRealTotal(t *testing.T) {
	uc, _, promos, _ := setupPriced(&strictCatalog{menu: realMenu()})
	promos.codes = map[string]int{"WELCOME10": 10}

	order, err := placeWith(uc, "WELCOME10", domain.NewOrderItemInput{MenuItemID: burgerID, MenuItemName: "Burger", Quantity: 1, UnitPriceCents: 1})

	require.NoError(t, err)
	assert.Equal(t, int64(129), order.DiscountCents, "10% of 12.99 €, not of the 1 cent the client claimed")
	assert.Equal(t, int64(1299-129), order.AmountDueCents())
}

func TestPlaceOrderAsksTheCatalogForTheOrdersRestaurant(t *testing.T) {
	catalog := &strictCatalog{menu: realMenu()}
	uc, _, _, _ := setupPriced(catalog)

	_, err := placeWith(uc, "", domain.NewOrderItemInput{MenuItemID: burgerID, MenuItemName: "Burger", Quantity: 1, UnitPriceCents: 1299})

	require.NoError(t, err)
	assert.Equal(t, []string{"resto-1"}, catalog.calls)
}
