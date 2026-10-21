package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goodfood/order-service/internal/domain"
)

// ── In-memory fakes for the domain ports ────────────────────

type fakeOrderRepo struct {
	orders map[string]*domain.Order
}

func newFakeOrderRepo() *fakeOrderRepo {
	return &fakeOrderRepo{orders: map[string]*domain.Order{}}
}

func (f *fakeOrderRepo) Create(_ context.Context, o *domain.Order) error {
	cp := *o
	f.orders[o.ID] = &cp
	return nil
}

func (f *fakeOrderRepo) GetByID(_ context.Context, id string) (*domain.Order, error) {
	o, ok := f.orders[id]
	if !ok {
		return nil, domain.NewNotFoundError("order not found")
	}
	cp := *o
	return &cp, nil
}

func (f *fakeOrderRepo) ListByCustomer(_ context.Context, customerID string) ([]domain.Order, error) {
	out := []domain.Order{}
	for _, o := range f.orders {
		if o.CustomerID == customerID {
			out = append(out, *o)
		}
	}
	return out, nil
}

func (f *fakeOrderRepo) ListByRestaurant(_ context.Context, restaurantID string) ([]domain.Order, error) {
	out := []domain.Order{}
	for _, o := range f.orders {
		if o.RestaurantID == restaurantID {
			out = append(out, *o)
		}
	}
	return out, nil
}

func (f *fakeOrderRepo) ListByStatus(_ context.Context, status domain.OrderStatus) ([]domain.Order, error) {
	out := []domain.Order{}
	for _, o := range f.orders {
		if o.Status == status {
			out = append(out, *o)
		}
	}
	return out, nil
}

func (f *fakeOrderRepo) UpdateStatus(_ context.Context, o *domain.Order) error {
	stored, ok := f.orders[o.ID]
	if !ok {
		return domain.NewNotFoundError("order not found")
	}
	stored.Status = o.Status
	stored.ConfirmedAt = o.ConfirmedAt
	return nil
}

// fakePayments stands in for payment-service (which owns Stripe).
type fakePayments struct {
	created     map[string]int64
	status      string
	createCalls int
}

func newFakePayments() *fakePayments {
	return &fakePayments{created: map[string]int64{}, status: "SUCCEEDED"}
}

func (f *fakePayments) CreateIntent(_ context.Context, _, orderID string, amountCents int64, currency string) (*domain.PaymentIntent, error) {
	f.createCalls++
	f.created[orderID] = amountCents
	return &domain.PaymentIntent{
		ID:           "pi_test_" + orderID,
		ClientSecret: "secret",
		Status:       "requires_payment_method",
		AmountCents:  amountCents,
		Currency:     currency,
	}, nil
}

func (f *fakePayments) Confirm(_ context.Context, _, orderID string) (string, error) {
	if _, ok := f.created[orderID]; !ok {
		return "", errors.New("no payment intent found for this order")
	}
	return f.status, nil
}

// fakePromos stands in for promo-service.
type fakePromos struct {
	codes        map[string]int // code -> percent off
	redeemCalls  int
	redeemedFor  map[string]bool // orderID already redeemed
	previewError error
}

func newFakePromos() *fakePromos {
	return &fakePromos{codes: map[string]int{}, redeemedFor: map[string]bool{}}
}

func (f *fakePromos) Preview(_ context.Context, _, code string, orderAmountCents int64) (*domain.PromoPreview, error) {
	if f.previewError != nil {
		return nil, f.previewError
	}
	percent, ok := f.codes[code]
	if !ok {
		return nil, errors.New("promo code not found")
	}
	return &domain.PromoPreview{Code: code, DiscountCents: orderAmountCents * int64(percent) / 100}, nil
}

func (f *fakePromos) Redeem(_ context.Context, _, code, orderID string, orderAmountCents int64) (int64, error) {
	f.redeemCalls++
	percent, ok := f.codes[code]
	if !ok {
		return 0, errors.New("promo code not found")
	}
	f.redeemedFor[orderID] = true
	return orderAmountCents * int64(percent) / 100, nil
}

// fakeRecipes stands in for menu-service recipes. Dishes absent from the map
// have no recipe.
type fakeRecipes struct {
	recipes map[string][]domain.StockLine
	err     error
	lookups []lookupCall
}

type lookupCall struct {
	restaurantID string
	menuItemIDs  []string
}

func newFakeRecipes() *fakeRecipes {
	return &fakeRecipes{recipes: map[string][]domain.StockLine{}}
}

func (f *fakeRecipes) Lookup(_ context.Context, restaurantID string, menuItemIDs []string) (map[string][]domain.StockLine, error) {
	f.lookups = append(f.lookups, lookupCall{restaurantID, menuItemIDs})
	if f.err != nil {
		return nil, f.err
	}
	return f.recipes, nil
}

// fakeStock stands in for stock-service; it records what it is asked.
type fakeStock struct {
	availability *domain.StockAvailability
	checkErr     error
	checkCalls   int
	checkedFor   string
	checkedLines []domain.StockLine
	consumeCalls int
	restoreCalls int
}

func newFakeStock() *fakeStock {
	return &fakeStock{availability: &domain.StockAvailability{Available: true}}
}

func (f *fakeStock) Check(_ context.Context, restaurantID string, lines []domain.StockLine) (*domain.StockAvailability, error) {
	f.checkCalls++
	f.checkedFor = restaurantID
	f.checkedLines = lines
	if f.checkErr != nil {
		return nil, f.checkErr
	}
	return f.availability, nil
}

func (f *fakeStock) Consume(context.Context, string, string, []domain.StockLine) error {
	f.consumeCalls++
	return nil
}

func (f *fakeStock) Restore(context.Context, string, string) error {
	f.restoreCalls++
	return nil
}

// ── Test fixtures ───────────────────────────────────────────

var (
	customer  = Actor{UserID: "cust-1", RoleSlugs: []string{RoleUser}}
	otherCust = Actor{UserID: "cust-2", RoleSlugs: []string{RoleUser}}
	manager   = Actor{UserID: "mgr-1", TenantID: "resto-1", RoleSlugs: []string{RoleManager}}
	admin     = Actor{UserID: "adm-1", RoleSlugs: []string{RoleAdmin}}
	courier   = Actor{UserID: "liv-1", RoleSlugs: []string{RoleCourier}}
)

func setup() (*UseCases, *fakeOrderRepo, *fakePayments, *fakePromos) {
	orders := newFakeOrderRepo()
	payments := newFakePayments()
	promos := newFakePromos()
	return NewUseCases(orders, payments, promos, domain.NoopPublisher{}, newFakeRecipes(), newFakeStock()), orders, payments, promos
}

// setupStock builds use cases whose recipes/stock fakes the test can drive.
func setupStock() (*UseCases, *fakeOrderRepo, *fakePromos, *fakeRecipes, *fakeStock) {
	orders := newFakeOrderRepo()
	promos := newFakePromos()
	recipes := newFakeRecipes()
	stock := newFakeStock()
	return NewUseCases(orders, newFakePayments(), promos, domain.NoopPublisher{}, recipes, stock), orders, promos, recipes, stock
}

func placeTestOrder(t *testing.T, uc *UseCases) *domain.Order {
	t.Helper()
	order, err := uc.PlaceOrder(context.Background(), customer, PlaceOrderInput{
		RestaurantID:    "resto-1",
		DeliveryAddress: "12 rue de Paris",
		Items: []domain.NewOrderItemInput{
			{MenuItemID: "m1", MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 1299},
		},
	})
	require.NoError(t, err)
	return order
}

// ── PlaceOrder ──────────────────────────────────────────────

func TestPlaceOrder(t *testing.T) {
	uc, orders, _, _ := setup()

	order := placeTestOrder(t, uc)
	assert.Equal(t, domain.StatusPlaced, order.Status)
	assert.Equal(t, int64(2598), order.TotalAmountCents)
	assert.Equal(t, customer.UserID, order.CustomerID)
	assert.Len(t, orders.orders, 1)
}

func TestPlaceOrderRequiresCustomerRole(t *testing.T) {
	uc, _, _, _ := setup()

	for _, actor := range []Actor{manager, courier, {UserID: "x"}} {
		_, err := uc.PlaceOrder(context.Background(), actor, PlaceOrderInput{})
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
	}
}

func TestPlaceOrderWithPromoCode(t *testing.T) {
	uc, _, _, promos := setup()
	promos.codes["WELCOME10"] = 10

	order, err := uc.PlaceOrder(context.Background(), customer, PlaceOrderInput{
		RestaurantID:    "resto-1",
		DeliveryAddress: "12 rue de Paris",
		Items: []domain.NewOrderItemInput{
			{MenuItemID: "m1", MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 1299},
		},
		PromoCode: "WELCOME10",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2598), order.TotalAmountCents, "the subtotal itself is never discounted")
	assert.Equal(t, int64(259), order.DiscountCents)
	assert.Equal(t, int64(2339), order.AmountDueCents())
	require.NotNil(t, order.PromoCode)
	assert.Equal(t, "WELCOME10", *order.PromoCode)
	assert.Equal(t, 0, promos.redeemCalls, "placing an order only previews — it never consumes a redemption")
}

func TestPlaceOrderRejectsUnknownPromoCode(t *testing.T) {
	uc, _, _, _ := setup()

	_, err := uc.PlaceOrder(context.Background(), customer, PlaceOrderInput{
		RestaurantID:    "resto-1",
		DeliveryAddress: "12 rue de Paris",
		Items: []domain.NewOrderItemInput{
			{MenuItemID: "m1", MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 1299},
		},
		PromoCode: "NOPE",
	})
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeValidation, derr.Code)
}

// ── PlaceOrder: stock check ─────────────────────────────────

func stockOrderInput(items ...domain.NewOrderItemInput) PlaceOrderInput {
	return PlaceOrderInput{RestaurantID: "resto-1", DeliveryAddress: "12 rue de Paris", Items: items}
}

func burger(qty int) domain.NewOrderItemInput {
	return domain.NewOrderItemInput{MenuItemID: "m1", MenuItemName: "Burger", Quantity: qty, UnitPriceCents: 1299}
}

func TestPlaceOrderRefusesWhenIngredientMissing(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["m1"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	stock.availability = &domain.StockAvailability{Available: false, Missing: []domain.MissingStock{
		{StockItemID: "steak", Name: "Steak", Unit: "kg", Required: 2, OnHand: 1},
	}}

	_, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(burger(2)))

	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeOutOfStock, derr.Code)
	assert.Equal(t, "Stock insuffisant pour « Burger » : Steak (besoin 2 kg, disponible 1)", derr.Message)
	assert.Equal(t, []domain.StockLine{{StockItemID: "steak", Quantity: 2}}, stock.checkedLines)
	assert.Empty(t, orders.orders, "nothing is created when stock is short")
}

func TestPlaceOrderSucceedsAndDoesNotTouchStockQuantities(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["m1"] = []domain.StockLine{{StockItemID: "steak", Quantity: 0.2}, {StockItemID: "bun", Quantity: 1}}
	recipes.recipes["m2"] = []domain.StockLine{{StockItemID: "steak", Quantity: 0.15}}

	order, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(
		burger(2),
		domain.NewOrderItemInput{MenuItemID: "m2", MenuItemName: "Double", Quantity: 1, UnitPriceCents: 1500},
	))

	require.NoError(t, err)
	assert.Len(t, orders.orders, 1)
	assert.Equal(t, domain.StatusPlaced, order.Status)
	assert.Equal(t, 1, stock.checkCalls)
	assert.Equal(t, "resto-1", stock.checkedFor)
	assert.Equal(t, []domain.StockLine{{StockItemID: "bun", Quantity: 2}, {StockItemID: "steak", Quantity: 0.55}}, stock.checkedLines)
	require.Len(t, recipes.lookups, 1)
	assert.Equal(t, "resto-1", recipes.lookups[0].restaurantID)
	assert.Equal(t, []string{"m1", "m2"}, recipes.lookups[0].menuItemIDs)
	assert.Equal(t, 0, stock.consumeCalls)
	assert.Equal(t, 0, stock.restoreCalls)
}

func TestPlaceOrderSkipsStockCallWhenNoDishHasARecipe(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()

	_, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(burger(1)))

	require.NoError(t, err)
	assert.Len(t, orders.orders, 1)
	assert.Len(t, recipes.lookups, 1)
	assert.Equal(t, 0, stock.checkCalls)
}

func TestPlaceOrderMixedRecipeAndUntrackedDishChecksOnlyTrackedIngredients(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["m2"] = []domain.StockLine{{StockItemID: "cola", Quantity: 0.33}}

	_, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(
		burger(3),
		domain.NewOrderItemInput{MenuItemID: "m2", MenuItemName: "Soda", Quantity: 2, UnitPriceCents: 300},
	))

	require.NoError(t, err)
	assert.Len(t, orders.orders, 1)
	assert.Equal(t, 1, stock.checkCalls)
	assert.Equal(t, []domain.StockLine{{StockItemID: "cola", Quantity: 0.66}}, stock.checkedLines)
}

func TestPlaceOrderRefusesWithUnavailableWhenRecipeLookupFails(t *testing.T) {
	uc, orders, promos, recipes, stock := setupStock()
	promos.codes["WELCOME10"] = 10
	recipes.err = errors.New("http://menu-service:8082 returned 500")
	in := stockOrderInput(burger(1))
	in.PromoCode = "WELCOME10"

	_, err := uc.PlaceOrder(context.Background(), customer, in)

	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeUnavailable, derr.Code)
	assert.Equal(t, "Service momentanément indisponible, veuillez réessayer dans quelques instants.", derr.Message)
	assert.NotContains(t, err.Error(), "returned 500")
	assert.Empty(t, orders.orders)
	assert.Equal(t, 0, stock.checkCalls)
}

func TestPlaceOrderRefusesWithUnavailableWhenStockCheckFails(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["m1"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	stock.checkErr = errors.New("http://stock-service:8083 returned 500")

	_, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(burger(1)))

	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeUnavailable, derr.Code)
	assert.Equal(t, "Service momentanément indisponible, veuillez réessayer dans quelques instants.", derr.Message)
	assert.NotContains(t, err.Error(), "returned 500")
	assert.Empty(t, orders.orders)
}

func TestPlaceOrderDoesNotPreviewPromoWhenStockIsShort(t *testing.T) {
	uc, orders, promos, recipes, stock := setupStock()
	promos.previewError = errors.New("preview must not be reached")
	recipes.recipes["m1"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	stock.availability = &domain.StockAvailability{Available: false}
	in := stockOrderInput(burger(1))
	in.PromoCode = "WELCOME10"

	_, err := uc.PlaceOrder(context.Background(), customer, in)

	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeOutOfStock, derr.Code, "stock is checked before the promo preview")
	assert.Equal(t, "Stock insuffisant", derr.Message)
	assert.Empty(t, orders.orders)
}

// ── GetOrder ────────────────────────────────────────────────

func TestGetOrderAccessRules(t *testing.T) {
	uc, _, _, _ := setup()
	order := placeTestOrder(t, uc)

	tests := []struct {
		name    string
		actor   Actor
		allowed bool
	}{
		{"owner", customer, true},
		{"admin", admin, true},
		{"courier", courier, true},
		{"manager of the restaurant", manager, true},
		{"manager of another restaurant", Actor{UserID: "mgr-2", TenantID: "resto-99", RoleSlugs: []string{RoleManager}}, false},
		{"another customer", otherCust, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := uc.GetOrder(context.Background(), tt.actor, order.ID)
			if tt.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestGetOrderNotFound(t *testing.T) {
	uc, _, _, _ := setup()
	_, err := uc.GetOrder(context.Background(), customer, "missing")
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeNotFound, derr.Code)
}

// ── ListCustomerOrders ──────────────────────────────────────

func TestListCustomerOrders(t *testing.T) {
	uc, _, _, _ := setup()
	placeTestOrder(t, uc)

	own, err := uc.ListCustomerOrders(context.Background(), customer, "")
	require.NoError(t, err)
	assert.Len(t, own, 1)

	// A customer cannot list someone else's orders...
	_, err = uc.ListCustomerOrders(context.Background(), otherCust, customer.UserID)
	require.Error(t, err)

	// ...but head office can.
	all, err := uc.ListCustomerOrders(context.Background(), admin, customer.UserID)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

// ── ListRestaurantOrders ────────────────────────────────────

func TestListRestaurantOrders(t *testing.T) {
	uc, _, _, _ := setup()
	placeTestOrder(t, uc)

	// The restaurant's manager sees its orders...
	list, err := uc.ListRestaurantOrders(context.Background(), manager, "resto-1")
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// ...another restaurant's manager does not.
	other := Actor{UserID: "mgr-2", TenantID: "resto-99", RoleSlugs: []string{RoleManager}}
	_, err = uc.ListRestaurantOrders(context.Background(), other, "resto-1")
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)

	// Customers cannot list restaurant orders at all.
	_, err = uc.ListRestaurantOrders(context.Background(), customer, "resto-1")
	require.Error(t, err)
}

// ── CreatePaymentIntent ─────────────────────────────────────

func TestCreatePaymentIntentFlow(t *testing.T) {
	uc, orders, payments, _ := setup()
	order := placeTestOrder(t, uc)

	res, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.TotalAmountCents, res.AmountCents)
	assert.Equal(t, "eur", res.Currency)
	assert.Equal(t, 1, payments.createCalls)

	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusPaymentPending, stored.Status)

	// Idempotency is now guaranteed by payment-service (see its own test
	// suite): order-service simply asks again and gets the same intent back.
	res2, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID)
	require.NoError(t, err)
	assert.Equal(t, res.IntentID, res2.IntentID)
}

func TestCreatePaymentIntentOnlyOwner(t *testing.T) {
	uc, _, _, _ := setup()
	order := placeTestOrder(t, uc)

	_, err := uc.CreatePaymentIntent(context.Background(), otherCust, order.ID)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
}

func TestCreatePaymentIntentWrongState(t *testing.T) {
	uc, orders, _, _ := setup()
	order := placeTestOrder(t, uc)
	// Force the order into a post-payment state.
	stored := orders.orders[order.ID]
	stored.Status = domain.StatusConfirmed

	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeConflict, derr.Code)
}

// ── ConfirmOrder ────────────────────────────────────────────

func TestConfirmOrderSuccess(t *testing.T) {
	uc, orders, _, _ := setup()
	order := placeTestOrder(t, uc)
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID)
	require.NoError(t, err)

	confirmed, err := uc.ConfirmOrder(context.Background(), customer, order.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusConfirmed, confirmed.Status)
	assert.NotNil(t, confirmed.ConfirmedAt)

	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusConfirmed, stored.Status)

	// Idempotent: confirming again returns the confirmed order.
	again, err := uc.ConfirmOrder(context.Background(), customer, order.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusConfirmed, again.Status)
}

func TestConfirmOrderRedeemsPromoOnlyOnce(t *testing.T) {
	uc, _, _, promos := setup()
	promos.codes["WELCOME10"] = 10

	order, err := uc.PlaceOrder(context.Background(), customer, PlaceOrderInput{
		RestaurantID:    "resto-1",
		DeliveryAddress: "12 rue de Paris",
		Items: []domain.NewOrderItemInput{
			{MenuItemID: "m1", MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 1299},
		},
		PromoCode: "WELCOME10",
	})
	require.NoError(t, err)

	intent, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.AmountDueCents(), intent.AmountCents, "payment-service is charged the discounted amount")

	confirmed, err := uc.ConfirmOrder(context.Background(), customer, order.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusConfirmed, confirmed.Status)
	assert.Equal(t, 1, promos.redeemCalls)
	assert.True(t, promos.redeemedFor[order.ID])

	// Confirming again is a no-op on an already-confirmed order — the
	// redemption is not attempted a second time.
	_, err = uc.ConfirmOrder(context.Background(), customer, order.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, promos.redeemCalls)
}

func TestConfirmOrderPaymentNotCompleted(t *testing.T) {
	uc, _, payments, _ := setup()
	order := placeTestOrder(t, uc)
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID)
	require.NoError(t, err)

	payments.status = "FAILED"
	_, err = uc.ConfirmOrder(context.Background(), customer, order.ID)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodePayment, derr.Code)
}

func TestConfirmOrderWithoutIntent(t *testing.T) {
	uc, _, _, _ := setup()
	order := placeTestOrder(t, uc)

	_, err := uc.ConfirmOrder(context.Background(), customer, order.ID)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodePayment, derr.Code)
}

// ── UpdateOrderStatus ───────────────────────────────────────

// confirmOrder moves a fresh order to CONFIRMED through the normal flow.
func confirmOrder(t *testing.T, uc *UseCases) *domain.Order {
	t.Helper()
	order := placeTestOrder(t, uc)
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID)
	require.NoError(t, err)
	confirmed, err := uc.ConfirmOrder(context.Background(), customer, order.ID)
	require.NoError(t, err)
	return confirmed
}

func TestUpdateOrderStatusByManager(t *testing.T) {
	uc, _, _, _ := setup()
	order := confirmOrder(t, uc)

	updated, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusInPreparation)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInPreparation, updated.Status)

	updated, err = uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusReadyForPickup)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusReadyForPickup, updated.Status)
}

func TestUpdateOrderStatusManagerWrongTenant(t *testing.T) {
	uc, _, _, _ := setup()
	order := confirmOrder(t, uc)

	wrongManager := Actor{UserID: "mgr-2", TenantID: "resto-99", RoleSlugs: []string{RoleManager}}
	_, err := uc.UpdateOrderStatus(context.Background(), wrongManager, order.ID, domain.StatusInPreparation)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
}

func TestUpdateOrderStatusCourierLimits(t *testing.T) {
	uc, _, _, _ := setup()
	order := confirmOrder(t, uc)

	// Couriers cannot do back-office transitions...
	_, err := uc.UpdateOrderStatus(context.Background(), courier, order.ID, domain.StatusInPreparation)
	require.Error(t, err)

	// ...but can pick up and deliver once the order is ready.
	_, err = uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusInPreparation)
	require.NoError(t, err)
	_, err = uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusReadyForPickup)
	require.NoError(t, err)

	updated, err := uc.UpdateOrderStatus(context.Background(), courier, order.ID, domain.StatusInDelivery)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInDelivery, updated.Status)

	updated, err = uc.UpdateOrderStatus(context.Background(), courier, order.ID, domain.StatusDelivered)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDelivered, updated.Status)
}

func TestUpdateOrderStatusInvalidTransition(t *testing.T) {
	uc, _, _, _ := setup()
	order := confirmOrder(t, uc)

	_, err := uc.UpdateOrderStatus(context.Background(), admin, order.ID, domain.StatusDelivered)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeInvalidTransition, derr.Code)
}

func TestUpdateOrderStatusCustomerForbidden(t *testing.T) {
	uc, _, _, _ := setup()
	order := confirmOrder(t, uc)

	_, err := uc.UpdateOrderStatus(context.Background(), customer, order.ID, domain.StatusInPreparation)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
}

// ── ListReadyForDelivery ────────────────────────────────────

func TestListReadyForDelivery(t *testing.T) {
	uc, orders, _, _ := setup()
	order := confirmOrder(t, uc)
	stored := orders.orders[order.ID]
	stored.Status = domain.StatusReadyForPickup

	list, err := uc.ListReadyForDelivery(context.Background(), courier)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	_, err = uc.ListReadyForDelivery(context.Background(), customer)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
}

// ── EventPublisher ──────────────────────────────────────────

type fakePublisher struct {
	events []domain.OrderReadyEvent
	err    error
}

func (f *fakePublisher) PublishOrderReady(_ context.Context, e domain.OrderReadyEvent) error {
	f.events = append(f.events, e)
	return f.err
}

func setupWithPublisher(pub domain.EventPublisher) *UseCases {
	return NewUseCases(newFakeOrderRepo(), newFakePayments(), newFakePromos(), pub, newFakeRecipes(), newFakeStock())
}

func TestUpdateOrderStatusPublishesOnlyWhenReady(t *testing.T) {
	pub := &fakePublisher{}
	uc := setupWithPublisher(pub)
	order := confirmOrder(t, uc)

	_, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusInPreparation)
	require.NoError(t, err)
	assert.Empty(t, pub.events, "no event before READY_FOR_PICKUP")

	_, err = uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusReadyForPickup)
	require.NoError(t, err)
	require.Len(t, pub.events, 1)
	got := pub.events[0]
	assert.Equal(t, order.ID, got.OrderID)
	assert.Equal(t, order.CustomerID, got.CustomerID)
	assert.Equal(t, order.RestaurantID, got.RestaurantID)
	assert.Equal(t, order.DeliveryAddress, got.DeliveryAddress)
	assert.NotEmpty(t, got.EventID)
	assert.False(t, got.OccurredAt.IsZero())

	_, err = uc.UpdateOrderStatus(context.Background(), courier, order.ID, domain.StatusInDelivery)
	require.NoError(t, err)
	assert.Len(t, pub.events, 1, "no event for later transitions")
}

func TestUpdateOrderStatusSucceedsWhenPublishFails(t *testing.T) {
	pub := &fakePublisher{err: errors.New("broker down")}
	uc := setupWithPublisher(pub)
	order := confirmOrder(t, uc)
	_, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusInPreparation)
	require.NoError(t, err)

	updated, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusReadyForPickup)

	require.NoError(t, err)
	assert.Equal(t, domain.StatusReadyForPickup, updated.Status)
	assert.Len(t, pub.events, 1)
}
