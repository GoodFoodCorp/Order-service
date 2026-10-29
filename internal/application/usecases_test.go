package application

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goodfood/order-service/internal/domain"
)

// ── In-memory fakes for the domain ports ────────────────────

type fakeOrderRepo struct {
	orders map[string]*domain.Order
	seq    map[string]int // insertion order, for deterministic listing
	// stockSyncErr makes UpdateStockSync fail (the state is then not persisted).
	stockSyncErr error
	// confirmWrites records each ConfirmWithStockSync call (one atomic write).
	confirmWrites []confirmWrite
	// events records what each status write put in the outbox.
	events []*domain.Event
	// beforeWrite, when set, runs once just before the next status write
	// (UpdateStatus / ConfirmWithStockSync / CancelWithStockSync), after the
	// use case has read the order: it simulates a concurrent request that
	// commits in between.
	beforeWrite func()
}

// interleave runs the one-shot beforeWrite hook, if any.
func (f *fakeOrderRepo) interleave() {
	if hook := f.beforeWrite; hook != nil {
		f.beforeWrite = nil
		hook()
	}
}

type confirmWrite struct {
	status    domain.OrderStatus
	stockSync domain.StockSyncState
}

func newFakeOrderRepo() *fakeOrderRepo {
	return &fakeOrderRepo{orders: map[string]*domain.Order{}, seq: map[string]int{}}
}

func (f *fakeOrderRepo) Create(_ context.Context, o *domain.Order) error {
	cp := *o
	f.orders[o.ID] = &cp
	f.seq[o.ID] = len(f.seq)
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

func (f *fakeOrderRepo) ListByStatuses(_ context.Context, statuses ...domain.OrderStatus) ([]domain.Order, error) {
	out := []domain.Order{}
	for _, o := range f.orders {
		for _, s := range statuses {
			if o.Status == s {
				out = append(out, *o)
				break
			}
		}
	}
	return out, nil
}

// UpdateStatus mirrors the guarded SQL: it writes only while the stored
// status equals from; on a mismatch, an already-applied target is a no-op and
// anything else a conflict.
func (f *fakeOrderRepo) UpdateStatus(_ context.Context, o *domain.Order, from domain.OrderStatus, event *domain.Event) error {
	f.interleave()
	stored, ok := f.orders[o.ID]
	if !ok {
		return domain.NewNotFoundError("order not found")
	}
	if stored.Status != from {
		if stored.Status == o.Status {
			return nil
		}
		return domain.NewConflictError("order changed concurrently, retry")
	}
	stored.Status = o.Status
	stored.ConfirmedAt = o.ConfirmedAt
	f.record(event)
	return nil
}

func (f *fakeOrderRepo) record(event *domain.Event) {
	if event != nil {
		f.events = append(f.events, event)
	}
}

func (f *fakeOrderRepo) eventTypes() []string {
	out := []string{}
	for _, e := range f.events {
		out = append(out, e.Type)
	}
	return out
}

// ConfirmWithStockSync mirrors `WHERE id = $4 AND status = 'PAYMENT_PENDING'`:
// zero rows (missing or other status) is applied=false, nothing written.
func (f *fakeOrderRepo) ConfirmWithStockSync(_ context.Context, o *domain.Order, state domain.StockSyncState, event *domain.Event) (bool, error) {
	f.interleave()
	stored, ok := f.orders[o.ID]
	if !ok || stored.Status != domain.StatusPaymentPending {
		return false, nil
	}
	stored.Status = o.Status
	stored.ConfirmedAt = o.ConfirmedAt
	stored.StockSync = state
	f.confirmWrites = append(f.confirmWrites, confirmWrite{o.Status, state})
	f.record(event)
	return true, nil
}

// stockRestorableStatuses mirrors the SQL CASE of CancelWithStockSync: the
// paid statuses from which a cancellation puts the ingredients back.
var stockRestorableStatuses = map[domain.OrderStatus]bool{
	domain.StatusConfirmed:      true,
	domain.StatusInPreparation:  true,
	domain.StatusReadyForPickup: true,
}

// CancelWithStockSync mirrors the SQL: guarded on a cancellable stored status,
// restorability computed from the stored (pre-update) status and stock_sync,
// RETURNING stock_sync and confirmed_at.
func (f *fakeOrderRepo) CancelWithStockSync(_ context.Context, o *domain.Order, event *domain.Event) error {
	f.interleave()
	stored, ok := f.orders[o.ID]
	if !ok {
		return domain.NewNotFoundError("order not found")
	}
	if !stored.CanTransitionTo(domain.StatusCancelled) {
		return domain.NewConflictError("order changed concurrently, retry")
	}
	if stockRestorableStatuses[stored.Status] &&
		(stored.StockSync == domain.StockSyncConsumed || stored.StockSync == domain.StockSyncConsumePending) {
		stored.StockSync = domain.StockSyncRestorePending
	}
	stored.Status = domain.StatusCancelled
	o.Status = domain.StatusCancelled
	o.StockSync = stored.StockSync
	o.ConfirmedAt = stored.ConfirmedAt
	f.record(event)
	return nil
}

func (f *fakeOrderRepo) UpdateStockSync(_ context.Context, orderID string, from, to domain.StockSyncState) (bool, error) {
	if f.stockSyncErr != nil {
		return false, f.stockSyncErr
	}
	stored, ok := f.orders[orderID]
	if !ok {
		return false, domain.NewNotFoundError("order not found")
	}
	if stored.StockSync != from {
		return false, nil
	}
	stored.StockSync = to
	return true, nil
}

func (f *fakeOrderRepo) ListByStockSync(_ context.Context, states []domain.StockSyncState, limit int) ([]domain.Order, error) {
	matching := []*domain.Order{}
	for _, o := range f.orders {
		for _, s := range states {
			if o.StockSync == s {
				matching = append(matching, o)
				break
			}
		}
	}
	sort.Slice(matching, func(i, j int) bool { return f.seq[matching[i].ID] < f.seq[matching[j].ID] })
	out := []domain.Order{}
	for _, o := range matching {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, *o)
	}
	return out, nil
}

func (f *fakeOrderRepo) AssignCourier(_ context.Context, orderID, courierID string) (bool, error) {
	stored, ok := f.orders[orderID]
	if !ok || !stored.IsClaimableByCourier() || (stored.CourierID != nil && *stored.CourierID != courierID) {
		return false, nil
	}
	stored.CourierID = &courierID
	return true, nil
}

func (f *fakeOrderRepo) UnassignCourier(_ context.Context, orderID, courierID string) error {
	if stored, ok := f.orders[orderID]; ok && stored.IsAssignedTo(courierID) && stored.IsClaimableByCourier() {
		stored.CourierID = nil
	}
	return nil
}

// fakePayments stands in for payment-service (which owns Stripe).
type fakePayments struct {
	created     map[string]domain.PaymentIntentRequest
	status      string
	createCalls int
	// paidCents, when set, is the amount payment-service reports as paid
	// (simulates a payment that does not match the order).
	paidCents int64
	refunds   []string
	refundErr error
}

func newFakePayments() *fakePayments {
	return &fakePayments{created: map[string]domain.PaymentIntentRequest{}, status: domain.PaymentStatusSucceeded}
}

func (f *fakePayments) CreateIntent(_ context.Context, req domain.PaymentIntentRequest) (*domain.PaymentIntent, error) {
	f.createCalls++
	f.created[req.OrderID] = req
	return &domain.PaymentIntent{
		ID:           "pi_test_" + req.OrderID,
		ClientSecret: "secret",
		Status:       "requires_payment_method",
		AmountCents:  req.AmountCents,
		Currency:     req.Currency,
	}, nil
}

func (f *fakePayments) GetPayment(_ context.Context, orderID string) (*domain.Payment, error) {
	req, ok := f.created[orderID]
	if !ok {
		return nil, domain.NewNotFoundError("no payment for this order")
	}
	amount := req.AmountCents
	if f.paidCents != 0 {
		amount = f.paidCents
	}
	return &domain.Payment{OrderID: orderID, CustomerID: req.CustomerID, Status: f.status, AmountCents: amount, Currency: req.Currency}, nil
}

func (f *fakePayments) Refund(_ context.Context, orderID string) error {
	f.refunds = append(f.refunds, orderID)
	return f.refundErr
}

// fakePromos stands in for promo-service.
type fakePromos struct {
	codes        map[string]int // code -> percent off
	redeemCalls  int
	redeemedFor  map[string]bool // orderID already redeemed
	previewError error
	released     []string
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

func (f *fakePromos) Redeem(_ context.Context, code, orderID, _ string, orderAmountCents int64) (int64, error) {
	f.redeemCalls++
	percent, ok := f.codes[code]
	if !ok {
		return 0, errors.New("promo code not found")
	}
	f.redeemedFor[orderID] = true
	return orderAmountCents * int64(percent) / 100, nil
}

func (f *fakePromos) Release(_ context.Context, orderID string) error {
	f.released = append(f.released, orderID)
	return nil
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
	consumeErr   error
	restoreErr   error
	consumeCalls int
	restoreCalls int
	consumed     []consumeCall
	restored     []restoreCall
	// ctxErrs holds ctx.Err() observed at each Consume/Restore call.
	ctxErrs []error
}

type consumeCall struct {
	orderID      string
	restaurantID string
	lines        []domain.StockLine
}

type restoreCall struct {
	orderID      string
	restaurantID string
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

func (f *fakeStock) Consume(ctx context.Context, orderID, restaurantID string, lines []domain.StockLine) error {
	f.consumeCalls++
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	f.consumed = append(f.consumed, consumeCall{orderID, restaurantID, lines})
	return f.consumeErr
}

func (f *fakeStock) Restore(ctx context.Context, orderID, restaurantID string) error {
	f.restoreCalls++
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	f.restored = append(f.restored, restoreCall{orderID, restaurantID})
	return f.restoreErr
}

// echoCatalog prices every line at what it already says: it keeps the older
// tests (which are about stock, promos, lifecycle…) independent of pricing.
// The pricing rules themselves are tested with strictCatalog.
type echoCatalog struct{}

func (echoCatalog) PriceItems(_ context.Context, _ string, items []domain.OrderItem) ([]domain.OrderItem, error) {
	return items, nil
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
	return NewUseCases(Deps{Orders: orders, Payments: payments, Promos: promos, Recipes: newFakeRecipes(), Stock: newFakeStock(), Catalog: echoCatalog{}}), orders, payments, promos
}

// setupStock builds use cases whose recipes/stock fakes the test can drive.
func setupStock() (*UseCases, *fakeOrderRepo, *fakePromos, *fakeRecipes, *fakeStock) {
	orders := newFakeOrderRepo()
	promos := newFakePromos()
	recipes := newFakeRecipes()
	stock := newFakeStock()
	return NewUseCases(Deps{Orders: orders, Payments: newFakePayments(), Promos: promos, Recipes: recipes, Stock: stock, Catalog: echoCatalog{}}), orders, promos, recipes, stock
}

func placeTestOrder(t *testing.T, uc *UseCases) *domain.Order {
	t.Helper()
	order, err := uc.PlaceOrder(context.Background(), customer, PlaceOrderInput{
		RestaurantID:    "resto-1",
		DeliveryAddress: "12 rue de Paris",
		Items: []domain.NewOrderItemInput{
			{MenuItemID: "11111111-1111-4111-8111-111111111111", MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 1299},
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
			{MenuItemID: "11111111-1111-4111-8111-111111111111", MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 1299},
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
			{MenuItemID: "11111111-1111-4111-8111-111111111111", MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 1299},
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
	return domain.NewOrderItemInput{MenuItemID: "11111111-1111-4111-8111-111111111111", MenuItemName: "Burger", Quantity: qty, UnitPriceCents: 1299}
}

func TestPlaceOrderRefusesWhenIngredientMissing(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
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
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 0.2}, {StockItemID: "bun", Quantity: 1}}
	recipes.recipes["22222222-2222-4222-8222-222222222222"] = []domain.StockLine{{StockItemID: "steak", Quantity: 0.15}}

	order, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(
		burger(2),
		domain.NewOrderItemInput{MenuItemID: "22222222-2222-4222-8222-222222222222", MenuItemName: "Double", Quantity: 1, UnitPriceCents: 1500},
	))

	require.NoError(t, err)
	assert.Len(t, orders.orders, 1)
	assert.Equal(t, domain.StatusPlaced, order.Status)
	assert.Equal(t, 1, stock.checkCalls)
	assert.Equal(t, "resto-1", stock.checkedFor)
	assert.Equal(t, []domain.StockLine{{StockItemID: "bun", Quantity: 2}, {StockItemID: "steak", Quantity: 0.55}}, stock.checkedLines)
	require.Len(t, recipes.lookups, 1)
	assert.Equal(t, "resto-1", recipes.lookups[0].restaurantID)
	assert.Equal(t, []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}, recipes.lookups[0].menuItemIDs)
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
	recipes.recipes["22222222-2222-4222-8222-222222222222"] = []domain.StockLine{{StockItemID: "cola", Quantity: 0.33}}

	_, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(
		burger(3),
		domain.NewOrderItemInput{MenuItemID: "22222222-2222-4222-8222-222222222222", MenuItemName: "Soda", Quantity: 2, UnitPriceCents: 300},
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
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
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
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
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
		{"courier not assigned to it", courier, false},
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

func TestGetOrderVisibleToItsAssignedCourierOnly(t *testing.T) {
	uc, orders, _, _ := setup()
	order := confirmOrder(t, uc)
	orders.orders[order.ID].Status = domain.StatusReadyForPickup
	_, err := uc.AssignCourier(context.Background(), order.ID, courier.UserID)
	require.NoError(t, err)

	_, err = uc.GetOrder(context.Background(), courier, order.ID)
	require.NoError(t, err)

	otherCourier := Actor{UserID: "liv-2", RoleSlugs: []string{RoleCourier}}
	_, err = uc.GetOrder(context.Background(), otherCourier, order.ID)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
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

	res, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	require.NoError(t, err)
	assert.Equal(t, order.TotalAmountCents, res.AmountCents)
	assert.Equal(t, "eur", res.Currency)
	assert.Equal(t, 1, payments.createCalls)

	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusPaymentPending, stored.Status)

	// Idempotency is now guaranteed by payment-service (see its own test
	// suite): order-service simply asks again and gets the same intent back.
	res2, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	require.NoError(t, err)
	assert.Equal(t, res.IntentID, res2.IntentID)
}

func TestCreatePaymentIntentOnlyOwner(t *testing.T) {
	uc, _, _, _ := setup()
	order := placeTestOrder(t, uc)

	_, err := uc.CreatePaymentIntent(context.Background(), otherCust, order.ID, "")
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

	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeConflict, derr.Code)
}

// ── ConfirmOrder ────────────────────────────────────────────

func TestConfirmOrderSuccess(t *testing.T) {
	uc, orders, _, _ := setup()
	order := placeTestOrder(t, uc)
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
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
			{MenuItemID: "11111111-1111-4111-8111-111111111111", MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 1299},
		},
		PromoCode: "WELCOME10",
	})
	require.NoError(t, err)

	intent, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
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
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
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
	assert.Equal(t, domain.ErrCodeConflict, derr.Code, "a PLACED order is not awaiting payment yet")
}

// The vulnerability being closed: paying less than the order (an intent
// created for another amount) must never confirm it.
func TestConfirmOrderRefusesPaymentOfAnotherAmount(t *testing.T) {
	uc, orders, payments, _ := setup()
	order := placeTestOrder(t, uc)
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	require.NoError(t, err)
	payments.paidCents = 1

	_, err = uc.ConfirmOrder(context.Background(), customer, order.ID)

	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodePayment, derr.Code)
	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusPaymentPending, stored.Status)
}

func TestCreatePaymentIntentSendsTheOrderAmountAndCustomer(t *testing.T) {
	uc, _, payments, _ := setup()
	order := placeTestOrder(t, uc)

	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "card-1")

	require.NoError(t, err)
	req := payments.created[order.ID]
	assert.Equal(t, order.AmountDueCents(), req.AmountCents)
	assert.Equal(t, customer.UserID, req.CustomerID)
	assert.Equal(t, "eur", req.Currency)
	assert.Equal(t, "card-1", req.PaymentMethodID)
}

func TestConfirmOrderSurvivesPromoRedemptionFailure(t *testing.T) {
	uc, _, _, promos := setup()
	promos.codes["WELCOME10"] = 10
	order, err := uc.PlaceOrder(context.Background(), customer, PlaceOrderInput{
		RestaurantID: "resto-1", DeliveryAddress: "12 rue de Paris", PromoCode: "WELCOME10",
		Items: []domain.NewOrderItemInput{{MenuItemID: "11111111-1111-4111-8111-111111111111", MenuItemName: "Burger", Quantity: 1, UnitPriceCents: 1000}},
	})
	require.NoError(t, err)
	_, err = uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	require.NoError(t, err)
	delete(promos.codes, "WELCOME10") // the code vanished between preview and payment

	confirmed, err := uc.ConfirmOrder(context.Background(), customer, order.ID)

	require.NoError(t, err, "the customer paid: the order is confirmed")
	assert.Equal(t, domain.StatusConfirmed, confirmed.Status)
}

// ── UpdateOrderStatus ───────────────────────────────────────

// confirmOrder moves a fresh order to CONFIRMED through the normal flow.
func confirmOrder(t *testing.T, uc *UseCases) *domain.Order {
	t.Helper()
	order := placeTestOrder(t, uc)
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
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

	// ...nor touch an order they did not claim...
	_, err = uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusInPreparation)
	require.NoError(t, err)
	_, err = uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusReadyForPickup)
	require.NoError(t, err)
	_, err = uc.UpdateOrderStatus(context.Background(), courier, order.ID, domain.StatusInDelivery)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)

	// ...but pick up and deliver the one assigned to them.
	_, err = uc.AssignCourier(context.Background(), order.ID, courier.UserID)
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

	list, err := uc.ListReadyForDelivery(context.Background(), admin)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// Couriers see claimable orders through delivery-service only.
	for _, actor := range []Actor{customer, courier} {
		_, err = uc.ListReadyForDelivery(context.Background(), actor)
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
	}

	internal, err := uc.ListClaimableOrders(context.Background())
	require.NoError(t, err)
	assert.Len(t, internal, 1)
}

// A courier can claim an order that is still cooking, so the travel time to
// the restaurant overlaps with preparation.
func TestListReadyForDeliveryIncludesOrdersStillInPreparation(t *testing.T) {
	uc, orders, _, _ := setup()
	order := confirmOrder(t, uc)
	stored := orders.orders[order.ID]
	stored.Status = domain.StatusInPreparation

	list, err := uc.ListReadyForDelivery(context.Background(), admin)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, domain.StatusInPreparation, list[0].Status)
}

// Orders that are not claimable yet (or no longer) must stay out of the list.
func TestListReadyForDeliveryExcludesOtherStatuses(t *testing.T) {
	uc, orders, _, _ := setup()
	order := confirmOrder(t, uc)
	stored := orders.orders[order.ID]

	for _, s := range []domain.OrderStatus{
		domain.StatusConfirmed,
		domain.StatusInDelivery,
		domain.StatusDelivered,
		domain.StatusCancelled,
	} {
		stored.Status = s
		list, err := uc.ListReadyForDelivery(context.Background(), admin)
		require.NoError(t, err)
		assert.Empty(t, list, "status %s must not be claimable", s)
	}
}

// ── Outbox events ───────────────────────────────────────────

func TestEveryLifecycleChangeWritesItsEvent(t *testing.T) {
	uc, orders, _, _ := setup()
	order := confirmOrder(t, uc)

	_, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusInPreparation)
	require.NoError(t, err)
	_, err = uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusReadyForPickup)
	require.NoError(t, err)
	_, err = uc.AssignCourier(context.Background(), order.ID, courier.UserID)
	require.NoError(t, err)
	_, err = uc.UpdateOrderStatus(context.Background(), courier, order.ID, domain.StatusInDelivery)
	require.NoError(t, err)

	assert.Equal(t, []string{"order.confirmed", "order.in_preparation", "order.ready_for_pickup", "order.in_delivery"},
		orders.eventTypes(), "PAYMENT_PENDING is technical and publishes nothing")

	ready := orders.events[2]
	assert.Equal(t, domain.EventOrderReadyForPickup, ready.Type)
	assert.Equal(t, order.ID, ready.Data.OrderID)
	assert.Equal(t, order.CustomerID, ready.Data.CustomerID)
	assert.Equal(t, order.RestaurantID, ready.Data.RestaurantID)
	assert.Equal(t, order.DeliveryAddress, ready.Data.DeliveryAddress)
	assert.NotEmpty(t, ready.ID)
	assert.False(t, ready.OccurredAt.IsZero())
	require.NotNil(t, orders.events[3].Data.CourierID)
	assert.Equal(t, courier.UserID, *orders.events[3].Data.CourierID)
}

func TestCancellationWritesItsEvent(t *testing.T) {
	uc, orders, _, _ := setup()
	order := confirmOrder(t, uc)

	_, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)

	require.NoError(t, err)
	assert.Equal(t, []string{"order.confirmed", "order.cancelled"}, orders.eventTypes())
}

func TestRejectedTransitionWritesNoEvent(t *testing.T) {
	uc, orders, _, _ := setup()
	order := confirmOrder(t, uc)

	_, err := uc.UpdateOrderStatus(context.Background(), admin, order.ID, domain.StatusDelivered)

	require.Error(t, err)
	assert.Equal(t, []string{"order.confirmed"}, orders.eventTypes())
}

// ── payment.succeeded ───────────────────────────────────────

func TestHandlePaymentSucceededConfirmsTheOrder(t *testing.T) {
	uc, orders, _, _ := setup()
	order := placeTestOrder(t, uc)
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	require.NoError(t, err)

	require.NoError(t, uc.HandlePaymentSucceeded(context.Background(), order.ID))
	require.NoError(t, uc.HandlePaymentSucceeded(context.Background(), order.ID), "redelivery is harmless")

	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusConfirmed, stored.Status)
	assert.Equal(t, []string{"order.confirmed"}, orders.eventTypes())
}

func TestHandlePaymentSucceededRetriesWhenPaymentCannotBeVerified(t *testing.T) {
	uc, _, payments, _ := setup()
	order := placeTestOrder(t, uc)
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	require.NoError(t, err)
	payments.status = "PENDING" // payment-service not caught up with the provider yet

	assert.Error(t, uc.HandlePaymentSucceeded(context.Background(), order.ID))
}

func TestHandlePaymentSucceededIgnoresFinalRefusals(t *testing.T) {
	uc, orders, _, _ := setup()
	order := placeTestOrder(t, uc)
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	require.NoError(t, err)
	_, err = uc.UpdateOrderStatus(context.Background(), admin, order.ID, domain.StatusCancelled)
	require.NoError(t, err)

	assert.NoError(t, uc.HandlePaymentSucceeded(context.Background(), order.ID), "a cancelled order is final")
	assert.NoError(t, uc.HandlePaymentSucceeded(context.Background(), "unknown-order"))
	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusCancelled, stored.Status)
}

// ── Stock sync: consume after payment, restore on cancellation ──

// placeStockOrder places a PLACED order with the given items (resto-1).
func placeStockOrder(t *testing.T, uc *UseCases, items ...domain.NewOrderItemInput) *domain.Order {
	t.Helper()
	order, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(items...))
	require.NoError(t, err)
	return order
}

func payAndConfirm(t *testing.T, uc *UseCases, orderID string) *domain.Order {
	t.Helper()
	_, err := uc.CreatePaymentIntent(context.Background(), customer, orderID, "")
	require.NoError(t, err)
	confirmed, err := uc.ConfirmOrder(context.Background(), customer, orderID)
	require.NoError(t, err)
	return confirmed
}

func storedSync(t *testing.T, orders *fakeOrderRepo, orderID string) domain.StockSyncState {
	t.Helper()
	stored, err := orders.GetByID(context.Background(), orderID)
	require.NoError(t, err)
	return stored.StockSync
}

func TestConfirmOrderConsumesCumulatedIngredientsAndMarksConsumed(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 0.2}, {StockItemID: "bun", Quantity: 1}}
	recipes.recipes["22222222-2222-4222-8222-222222222222"] = []domain.StockLine{{StockItemID: "bun", Quantity: 1}}
	order := placeStockOrder(t, uc, burger(2),
		domain.NewOrderItemInput{MenuItemID: "22222222-2222-4222-8222-222222222222", MenuItemName: "Petit pain", Quantity: 3, UnitPriceCents: 300},
		domain.NewOrderItemInput{MenuItemID: "33333333-3333-4333-8333-333333333333", MenuItemName: "Eau", Quantity: 1, UnitPriceCents: 200})

	confirmed := payAndConfirm(t, uc, order.ID)

	assert.Equal(t, domain.StatusConfirmed, confirmed.Status)
	assert.Equal(t, domain.StockSyncConsumed, confirmed.StockSync)
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, order.ID))
	assert.Equal(t, 1, stock.consumeCalls)
	assert.Equal(t, []consumeCall{{
		orderID: order.ID, restaurantID: "resto-1",
		lines: []domain.StockLine{{StockItemID: "bun", Quantity: 5}, {StockItemID: "steak", Quantity: 0.4}},
	}}, stock.consumed, "bun cumulated over two dishes (2+3), untracked dish m3 ignored")
}

func TestConfirmOrderKeepsOrderConfirmedAndMarksConsumePendingWhenStockDown(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	stock.consumeErr = errors.New("stock-service unreachable: secret-detail")
	order := placeStockOrder(t, uc, burger(1))

	confirmed := payAndConfirm(t, uc, order.ID)

	assert.Equal(t, domain.StatusConfirmed, confirmed.Status)
	assert.Equal(t, domain.StockSyncConsumePending, confirmed.StockSync)
	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusConfirmed, stored.Status)
	assert.Equal(t, domain.StockSyncConsumePending, stored.StockSync)
	assert.Equal(t, 1, stock.consumeCalls)
	assert.Equal(t, []confirmWrite{{domain.StatusConfirmed, domain.StockSyncConsumePending}}, orders.confirmWrites,
		"status and the pending intent are written in one call")
}

func TestConfirmOrderMarksConsumePendingWhenRecipeLookupFails(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	order := placeStockOrder(t, uc, burger(1))
	recipes.err = errors.New("menu-service down")

	confirmed := payAndConfirm(t, uc, order.ID)

	assert.Equal(t, domain.StatusConfirmed, confirmed.Status)
	assert.Equal(t, domain.StockSyncConsumePending, storedSync(t, orders, order.ID))
	assert.Equal(t, domain.StockSyncConsumePending, confirmed.StockSync)
	assert.Equal(t, 0, stock.consumeCalls)
}

func TestConfirmOrderWithoutAnyRecipeLeavesStateNoneAndNeverCallsStock(t *testing.T) {
	uc, orders, _, _, stock := setupStock()
	order := placeStockOrder(t, uc, burger(1))

	confirmed := payAndConfirm(t, uc, order.ID)

	assert.Equal(t, domain.StockSyncNone, confirmed.StockSync)
	assert.Equal(t, domain.StockSyncNone, storedSync(t, orders, order.ID))
	assert.Equal(t, 0, stock.consumeCalls)
	assert.Equal(t, 0, stock.restoreCalls)
}

func TestConfirmOrderAlreadyConfirmedDoesNotConsumeAgain(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	order := placeStockOrder(t, uc, burger(1))
	payAndConfirm(t, uc, order.ID)
	lookups := len(recipes.lookups)

	again, err := uc.ConfirmOrder(context.Background(), customer, order.ID)

	require.NoError(t, err)
	assert.Equal(t, domain.StatusConfirmed, again.Status)
	assert.Equal(t, 1, stock.consumeCalls, "replayed confirmation must not consume twice")
	assert.Equal(t, lookups, len(recipes.lookups))
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, order.ID))
}

func TestConfirmOrderKeepsConsumePendingWhenConsumedWriteFails(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	order := placeStockOrder(t, uc, burger(1))
	orders.stockSyncErr = errors.New("db down")

	confirmed := payAndConfirm(t, uc, order.ID)

	assert.Equal(t, domain.StatusConfirmed, confirmed.Status)
	assert.Equal(t, 1, stock.consumeCalls)
	assert.Equal(t, domain.StockSyncConsumePending, confirmed.StockSync, "in-memory state follows what was persisted")
	assert.Equal(t, domain.StockSyncConsumePending, storedSync(t, orders, order.ID), "replay-safe: the worker will retry the idempotent consume")
}

func TestConsumeStockLostCASDoesNotOverwriteConcurrentCancel(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	order := placeStockOrder(t, uc, burger(1))
	orders.orders[order.ID].StockSync = domain.StockSyncRestorePending // a cancel won the race
	snapshot, _ := orders.GetByID(context.Background(), order.ID)
	snapshot.StockSync = domain.StockSyncConsumePending // stale view held by the confirming request

	uc.consumeStock(context.Background(), snapshot)

	assert.Equal(t, 1, stock.consumeCalls)
	assert.Equal(t, domain.StockSyncRestorePending, storedSync(t, orders, order.ID))
	assert.Equal(t, domain.StockSyncConsumePending, snapshot.StockSync, "in-memory state not advanced on a lost CAS")
}

func TestConsumeStockRunsOnDetachedContext(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	order := placeStockOrder(t, uc, burger(1))
	orders.orders[order.ID].StockSync = domain.StockSyncConsumePending
	snapshot, _ := orders.GetByID(context.Background(), order.ID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	uc.consumeStock(ctx, snapshot)

	require.Len(t, stock.ctxErrs, 1)
	assert.NoError(t, stock.ctxErrs[0], "the Consume call must not see the cancelled request context")
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, order.ID))
}

func TestListByStockSyncIsOldestFirst(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	stock.consumeErr = errors.New("down")
	var ids []string
	for i := 0; i < 3; i++ {
		o := placeStockOrder(t, uc, burger(1))
		payAndConfirm(t, uc, o.ID)
		ids = append(ids, o.ID)
	}

	got, err := orders.ListByStockSync(context.Background(), []domain.StockSyncState{domain.StockSyncConsumePending}, 2)

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, ids[0], got[0].ID)
	assert.Equal(t, ids[1], got[1].ID)
}

func TestCancelFromPaidStatusRestoresStock(t *testing.T) {
	for _, via := range []struct {
		name  string
		steps []domain.OrderStatus
	}{
		{"CONFIRMED", nil},
		{"IN_PREPARATION", []domain.OrderStatus{domain.StatusInPreparation}},
		{"READY_FOR_PICKUP", []domain.OrderStatus{domain.StatusInPreparation, domain.StatusReadyForPickup}},
	} {
		t.Run(via.name, func(t *testing.T) {
			uc, orders, _, recipes, stock := setupStock()
			recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
			order := placeStockOrder(t, uc, burger(1))
			payAndConfirm(t, uc, order.ID)
			for _, step := range via.steps {
				_, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, step)
				require.NoError(t, err)
			}

			cancelled, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)

			require.NoError(t, err)
			assert.Equal(t, domain.StatusCancelled, cancelled.Status)
			assert.Equal(t, domain.StockSyncRestored, cancelled.StockSync)
			assert.Equal(t, domain.StockSyncRestored, storedSync(t, orders, order.ID))
			assert.Equal(t, 1, stock.consumeCalls)
			assert.Equal(t, []restoreCall{{orderID: order.ID, restaurantID: "resto-1"}}, stock.restored)
		})
	}
}

func TestCancelBeforePaymentNeverCallsStock(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "PLACED"
		if pending {
			name = "PAYMENT_PENDING"
		}
		t.Run(name, func(t *testing.T) {
			uc, orders, _, recipes, stock := setupStock()
			recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
			order := placeStockOrder(t, uc, burger(1))
			if pending {
				_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
				require.NoError(t, err)
			}

			cancelled, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)

			require.NoError(t, err)
			assert.Equal(t, domain.StatusCancelled, cancelled.Status)
			assert.Equal(t, domain.StockSyncNone, storedSync(t, orders, order.ID))
			assert.Equal(t, 0, stock.consumeCalls)
			assert.Equal(t, 0, stock.restoreCalls)
		})
	}
}

func TestCancelWithConsumePendingStillCallsRestore(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	stock.consumeErr = errors.New("timeout")
	order := placeStockOrder(t, uc, burger(1))
	payAndConfirm(t, uc, order.ID)
	require.Equal(t, domain.StockSyncConsumePending, storedSync(t, orders, order.ID))

	cancelled, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)

	require.NoError(t, err)
	assert.Equal(t, domain.StockSyncRestored, cancelled.StockSync)
	assert.Equal(t, domain.StockSyncRestored, storedSync(t, orders, order.ID))
	assert.Equal(t, []restoreCall{{orderID: order.ID, restaurantID: "resto-1"}}, stock.restored,
		"restore is sent so stock-service records a tombstone against the delayed consume")
}

func TestCancelMarksRestorePendingWhenStockDown(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	order := placeStockOrder(t, uc, burger(1))
	payAndConfirm(t, uc, order.ID)
	stock.restoreErr = errors.New("stock-service unreachable")

	cancelled, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)

	require.NoError(t, err)
	assert.Equal(t, domain.StatusCancelled, cancelled.Status)
	assert.Equal(t, domain.StockSyncRestorePending, cancelled.StockSync)
	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusCancelled, stored.Status)
	assert.Equal(t, domain.StockSyncRestorePending, stored.StockSync)
	assert.Equal(t, 1, stock.restoreCalls)
}

func TestCancelWithStateNoneDoesNotCallStock(t *testing.T) {
	uc, orders, _, _, stock := setupStock()
	order := placeStockOrder(t, uc, burger(1))
	payAndConfirm(t, uc, order.ID) // no recipe -> NONE

	cancelled, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)

	require.NoError(t, err)
	assert.Equal(t, domain.StatusCancelled, cancelled.Status)
	assert.Equal(t, domain.StockSyncNone, storedSync(t, orders, order.ID))
	assert.Equal(t, 0, stock.restoreCalls)
}

func TestNonCancelTransitionNeverRestores(t *testing.T) {
	uc, _, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	order := placeStockOrder(t, uc, burger(1))
	payAndConfirm(t, uc, order.ID)

	_, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusInPreparation)

	require.NoError(t, err)
	assert.Equal(t, 0, stock.restoreCalls)
}

func TestCancelKeepsRestorePendingWhenRestoredWriteFails(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	order := placeStockOrder(t, uc, burger(1))
	payAndConfirm(t, uc, order.ID)
	require.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, order.ID))
	orders.stockSyncErr = errors.New("db down")

	cancelled, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)

	require.NoError(t, err)
	assert.Equal(t, 1, stock.restoreCalls)
	assert.Equal(t, domain.StockSyncRestorePending, cancelled.StockSync)
	assert.Equal(t, domain.StockSyncRestorePending, storedSync(t, orders, order.ID), "restore is idempotent, the worker replays it")
}

// ── RetryStockSync ──────────────────────────────────────────

// pendingOrder places an order and forces its persisted status and stock sync
// state, as a crash or an outage would have left it.
func pendingOrder(t *testing.T, uc *UseCases, orders *fakeOrderRepo, status domain.OrderStatus, sync domain.StockSyncState) string {
	t.Helper()
	order := placeStockOrder(t, uc, burger(1))
	orders.orders[order.ID].Status = status
	orders.orders[order.ID].StockSync = sync
	return order.ID
}

func TestRetryConsumesPendingOrderOnceStockIsBack(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 0.2}, {StockItemID: "bun", Quantity: 1}}
	stock.consumeErr = errors.New("down")
	order := placeStockOrder(t, uc, burger(2))
	payAndConfirm(t, uc, order.ID)
	require.Equal(t, domain.StockSyncConsumePending, storedSync(t, orders, order.ID))
	stock.consumeErr = nil
	stock.consumed = nil
	stock.consumeCalls = 0

	processed := uc.RetryStockSync(context.Background(), 50)

	assert.Equal(t, 1, processed)
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, order.ID))
	assert.Equal(t, []consumeCall{{
		orderID: order.ID, restaurantID: "resto-1",
		lines: []domain.StockLine{{StockItemID: "bun", Quantity: 2}, {StockItemID: "steak", Quantity: 0.4}},
	}}, stock.consumed)
	assert.Equal(t, 0, stock.restoreCalls)
}

func TestRetryRestoresPendingCancelledOrder(t *testing.T) {
	uc, orders, _, _, stock := setupStock()
	id := pendingOrder(t, uc, orders, domain.StatusCancelled, domain.StockSyncRestorePending)

	processed := uc.RetryStockSync(context.Background(), 50)

	assert.Equal(t, 1, processed)
	assert.Equal(t, domain.StockSyncRestored, storedSync(t, orders, id))
	assert.Equal(t, []restoreCall{{orderID: id, restaurantID: "resto-1"}}, stock.restored)
	assert.Equal(t, 0, stock.consumeCalls)
}

func TestRetryLeavesStateWhenStockStillDown(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	stock.consumeErr = errors.New("down")
	consumeID := pendingOrder(t, uc, orders, domain.StatusConfirmed, domain.StockSyncConsumePending)
	restoreID := pendingOrder(t, uc, orders, domain.StatusCancelled, domain.StockSyncRestorePending)
	consume2ID := pendingOrder(t, uc, orders, domain.StatusConfirmed, domain.StockSyncConsumePending)

	processed := uc.RetryStockSync(context.Background(), 50)

	assert.Equal(t, 3, processed)
	assert.Equal(t, domain.StockSyncConsumePending, storedSync(t, orders, consumeID))
	assert.Equal(t, domain.StockSyncConsumePending, storedSync(t, orders, consume2ID))
	assert.Equal(t, domain.StockSyncRestored, storedSync(t, orders, restoreID), "a failing consume does not stop other orders")
	assert.Equal(t, 2, stock.consumeCalls)
	assert.Equal(t, 1, stock.restoreCalls)
}

func TestRetryKeepsRestorePendingWhenRestoreFails(t *testing.T) {
	uc, orders, _, _, stock := setupStock()
	stock.restoreErr = errors.New("down")
	id := pendingOrder(t, uc, orders, domain.StatusCancelled, domain.StockSyncRestorePending)

	processed := uc.RetryStockSync(context.Background(), 50)

	assert.Equal(t, 1, processed)
	assert.Equal(t, domain.StockSyncRestorePending, storedSync(t, orders, id))
	assert.Equal(t, 1, stock.restoreCalls)
}

func TestRetryIgnoresOrdersInFinalStates(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	none := pendingOrder(t, uc, orders, domain.StatusConfirmed, domain.StockSyncNone)
	consumed := pendingOrder(t, uc, orders, domain.StatusConfirmed, domain.StockSyncConsumed)
	restored := pendingOrder(t, uc, orders, domain.StatusCancelled, domain.StockSyncRestored)
	lookups := len(recipes.lookups)

	processed := uc.RetryStockSync(context.Background(), 50)

	assert.Equal(t, 0, processed)
	assert.Equal(t, domain.StockSyncNone, storedSync(t, orders, none))
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, consumed))
	assert.Equal(t, domain.StockSyncRestored, storedSync(t, orders, restored))
	assert.Equal(t, 0, stock.consumeCalls)
	assert.Equal(t, 0, stock.restoreCalls)
	assert.Equal(t, lookups, len(recipes.lookups))
}

func TestRetryConsumePendingWithNoRecipeLinesBecomesNone(t *testing.T) {
	uc, orders, _, _, stock := setupStock()
	id := pendingOrder(t, uc, orders, domain.StatusConfirmed, domain.StockSyncConsumePending)

	processed := uc.RetryStockSync(context.Background(), 50)

	assert.Equal(t, 1, processed)
	assert.Equal(t, domain.StockSyncNone, storedSync(t, orders, id))
	assert.Equal(t, 0, stock.consumeCalls)
	assert.Equal(t, 0, stock.restoreCalls)
}

func TestRetryCancelledOrderWithConsumePendingConvertsToRestore(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	id := pendingOrder(t, uc, orders, domain.StatusCancelled, domain.StockSyncConsumePending)

	processed := uc.RetryStockSync(context.Background(), 50)

	assert.Equal(t, 1, processed)
	assert.Equal(t, 0, stock.consumeCalls, "a cancelled order must never replay its consume")
	assert.Equal(t, []restoreCall{{orderID: id, restaurantID: "resto-1"}}, stock.restored)
	assert.Equal(t, domain.StockSyncRestored, storedSync(t, orders, id))
}

func TestRetryCancelledOrderWithConsumePendingStaysRestorePendingWhenStockDown(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	stock.restoreErr = errors.New("down")
	id := pendingOrder(t, uc, orders, domain.StatusCancelled, domain.StockSyncConsumePending)

	uc.RetryStockSync(context.Background(), 50)

	assert.Equal(t, 0, stock.consumeCalls)
	assert.Equal(t, domain.StockSyncRestorePending, storedSync(t, orders, id), "converted, so the next pass only restores")
}

func TestRetryIsIdempotentWhenRunTwice(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	consumeID := pendingOrder(t, uc, orders, domain.StatusConfirmed, domain.StockSyncConsumePending)
	restoreID := pendingOrder(t, uc, orders, domain.StatusCancelled, domain.StockSyncRestorePending)

	first := uc.RetryStockSync(context.Background(), 50)
	second := uc.RetryStockSync(context.Background(), 50)

	assert.Equal(t, 2, first)
	assert.Equal(t, 0, second)
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, consumeID))
	assert.Equal(t, domain.StockSyncRestored, storedSync(t, orders, restoreID))
	assert.Equal(t, 1, stock.consumeCalls)
	assert.Equal(t, 1, stock.restoreCalls)
}

func TestRetryRespectsLimit(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, pendingOrder(t, uc, orders, domain.StatusConfirmed, domain.StockSyncConsumePending))
	}

	first := uc.RetryStockSync(context.Background(), 2)

	assert.Equal(t, 2, first)
	assert.Equal(t, 2, stock.consumeCalls)
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, ids[0]))
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, ids[1]))
	assert.Equal(t, domain.StockSyncConsumePending, storedSync(t, orders, ids[2]), "oldest first, the rest waits")

	second := uc.RetryStockSync(context.Background(), 2)

	assert.Equal(t, 1, second)
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, ids[2]))
}

func TestRetryStartsNoWorkOnCancelledContext(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
	id := pendingOrder(t, uc, orders, domain.StatusConfirmed, domain.StockSyncConsumePending)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	processed := uc.RetryStockSync(ctx, 50)

	assert.Equal(t, 0, processed)
	assert.Equal(t, 0, stock.consumeCalls)
	assert.Equal(t, 0, stock.restoreCalls)
	assert.Equal(t, domain.StockSyncConsumePending, storedSync(t, orders, id))
}

func TestPlaceOrderCanonicalisesMenuItemIDBeforeStockCheck(t *testing.T) {
	const canonical = "11111111-1111-4111-8111-111111111abc"
	forms := map[string]string{
		"uppercase": "11111111-1111-4111-8111-111111111ABC",
		"urn":       "urn:uuid:11111111-1111-4111-8111-111111111abc",
		"braces":    "{11111111-1111-4111-8111-111111111abc}",
		"no hyphen": "11111111111141118111111111111abc",
		"padded":    " 11111111-1111-4111-8111-111111111ABC ",
	}
	for name, form := range forms {
		t.Run(name, func(t *testing.T) {
			uc, orders, _, recipes, stock := setupStock()
			recipes.recipes[canonical] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
			in := domain.NewOrderItemInput{MenuItemID: form, MenuItemName: "Burger", Quantity: 100, UnitPriceCents: 1299}

			order, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(in))

			require.NoError(t, err)
			assert.Equal(t, canonical, order.Items[0].MenuItemID)
			stored, err := orders.GetByID(context.Background(), order.ID)
			require.NoError(t, err)
			assert.Equal(t, canonical, stored.Items[0].MenuItemID)
			assert.Equal(t, 1, stock.checkCalls, "the recipe is found, so the stock check applies")
			assert.Equal(t, []domain.StockLine{{StockItemID: "steak", Quantity: 100}}, stock.checkedLines)
			assert.Equal(t, []string{canonical}, recipes.lookups[0].menuItemIDs)
		})
	}
}

func TestPlaceOrderRejectsNonUUIDMenuItemID(t *testing.T) {
	uc, orders, _, _, stock := setupStock()
	in := domain.NewOrderItemInput{MenuItemID: "m1", MenuItemName: "Burger", Quantity: 1, UnitPriceCents: 1299}

	_, err := uc.PlaceOrder(context.Background(), customer, stockOrderInput(in))

	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCodeValidation, de.Code)
	assert.Equal(t, "menu item id must be a valid UUID", de.Message)
	assert.Empty(t, orders.orders)
	assert.Equal(t, 0, stock.checkCalls)
}

// ── Concurrent status writes (guarded UPDATEs) ──────────────

func steakRecipe(recipes *fakeRecipes) {
	recipes.recipes["11111111-1111-4111-8111-111111111111"] = []domain.StockLine{{StockItemID: "steak", Quantity: 1}}
}

// Scenario (a): the cancel was built from a PAYMENT_PENDING read, but the
// confirmation (and possibly its consume) committed before the cancel write.
func TestCancelBuiltFromStalePaymentPendingReadStillRestores(t *testing.T) {
	for _, consumeDown := range []bool{false, true} {
		name := "consume applied"
		if consumeDown {
			name = "consume still pending"
		}
		t.Run(name, func(t *testing.T) {
			uc, orders, _, recipes, stock := setupStock()
			steakRecipe(recipes)
			if consumeDown {
				stock.consumeErr = errors.New("down")
			}
			order := placeStockOrder(t, uc, burger(1))
			_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
			require.NoError(t, err)
			orders.beforeWrite = func() { payAndConfirm(t, uc, order.ID) }

			cancelled, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)

			require.NoError(t, err)
			assert.Equal(t, domain.StatusCancelled, cancelled.Status)
			assert.Equal(t, domain.StockSyncRestored, cancelled.StockSync)
			stored, _ := orders.GetByID(context.Background(), order.ID)
			assert.Equal(t, domain.StatusCancelled, stored.Status)
			assert.Equal(t, domain.StockSyncRestored, stored.StockSync)
			assert.Equal(t, 1, stock.consumeCalls)
			assert.Equal(t, []restoreCall{{orderID: order.ID, restaurantID: "resto-1"}}, stock.restored)
		})
	}
}

// Scenario (b): a slow duplicate confirmation read PAYMENT_PENDING; meanwhile
// the order was confirmed, cancelled and restored.
func TestLateDuplicateConfirmAfterCancelDoesNotResetTheOrder(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	steakRecipe(recipes)
	order := placeStockOrder(t, uc, burger(1))
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	require.NoError(t, err)
	orders.beforeWrite = func() {
		payAndConfirm(t, uc, order.ID)
		_, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)
		require.NoError(t, err)
	}

	_, err = uc.ConfirmOrder(context.Background(), customer, order.ID)

	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCodeConflict, de.Code)
	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusCancelled, stored.Status)
	assert.Equal(t, domain.StockSyncRestored, stored.StockSync)
	assert.Equal(t, 1, stock.consumeCalls, "the late confirmation must not consume")
	assert.Equal(t, 1, stock.restoreCalls)
}

func TestConcurrentDuplicateConfirmIsIdempotentAndConsumesOnce(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	steakRecipe(recipes)
	order := placeStockOrder(t, uc, burger(1))
	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")
	require.NoError(t, err)
	orders.beforeWrite = func() { payAndConfirm(t, uc, order.ID) }

	again, err := uc.ConfirmOrder(context.Background(), customer, order.ID)

	require.NoError(t, err)
	assert.Equal(t, domain.StatusConfirmed, again.Status)
	assert.Equal(t, domain.StockSyncConsumed, again.StockSync, "returns the persisted order")
	assert.Equal(t, domain.StockSyncConsumed, storedSync(t, orders, order.ID))
	assert.Equal(t, 1, stock.consumeCalls)
	assert.Len(t, orders.confirmWrites, 1, "only the first confirmation is written")
}

func TestCancelConflictsWhenStatusMovedPastCancellable(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	steakRecipe(recipes)
	order := placeStockOrder(t, uc, burger(1))
	payAndConfirm(t, uc, order.ID)
	orders.beforeWrite = func() { orders.orders[order.ID].Status = domain.StatusInDelivery }

	_, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusCancelled)

	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCodeConflict, de.Code)
	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusInDelivery, stored.Status)
	assert.Equal(t, domain.StockSyncConsumed, stored.StockSync)
	assert.Equal(t, 0, stock.restoreCalls)
}

func TestStatusUpdateDoesNotResurrectConcurrentlyCancelledOrder(t *testing.T) {
	uc, orders, _, recipes, stock := setupStock()
	steakRecipe(recipes)
	order := placeStockOrder(t, uc, burger(1))
	payAndConfirm(t, uc, order.ID)
	orders.beforeWrite = func() {
		_, err := uc.UpdateOrderStatus(context.Background(), admin, order.ID, domain.StatusCancelled)
		require.NoError(t, err)
	}

	_, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusInPreparation)

	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, domain.ErrCodeConflict, de.Code)
	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusCancelled, stored.Status)
	assert.Equal(t, domain.StockSyncRestored, stored.StockSync)
	assert.Equal(t, 1, stock.restoreCalls)
}

func TestStatusUpdateAlreadyAppliedConcurrentlyIsNotAConflict(t *testing.T) {
	uc, orders, _, _, _ := setupStock()
	order := placeStockOrder(t, uc, burger(1))
	orders.beforeWrite = func() { orders.orders[order.ID].Status = domain.StatusPaymentPending }

	_, err := uc.CreatePaymentIntent(context.Background(), customer, order.ID, "")

	require.NoError(t, err)
	stored, _ := orders.GetByID(context.Background(), order.ID)
	assert.Equal(t, domain.StatusPaymentPending, stored.Status)
}

// ── Courier assignment (delivery-service internal API) ──────

func readyOrder(t *testing.T, uc *UseCases, orders *fakeOrderRepo) *domain.Order {
	t.Helper()
	order := confirmOrder(t, uc)
	orders.orders[order.ID].Status = domain.StatusReadyForPickup
	return order
}

func TestAssignCourierIsExclusiveAndIdempotent(t *testing.T) {
	uc, orders, _, _ := setup()
	order := readyOrder(t, uc, orders)

	assigned, err := uc.AssignCourier(context.Background(), order.ID, "liv-1")
	require.NoError(t, err)
	assert.True(t, assigned.IsAssignedTo("liv-1"))

	_, err = uc.AssignCourier(context.Background(), order.ID, "liv-1")
	require.NoError(t, err, "same courier again is a no-op")

	_, err = uc.AssignCourier(context.Background(), order.ID, "liv-2")
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeConflict, derr.Code)
}

func TestAssignCourierRefusedOutsideClaimableStatuses(t *testing.T) {
	uc, _, _, _ := setup()
	order := confirmOrder(t, uc) // CONFIRMED: kitchen has not started

	_, err := uc.AssignCourier(context.Background(), order.ID, "liv-1")
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeConflict, derr.Code)

	_, err = uc.AssignCourier(context.Background(), order.ID, "")
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeValidation, derr.Code)
}

func TestUnassignCourierFreesTheOrder(t *testing.T) {
	uc, orders, _, _ := setup()
	order := readyOrder(t, uc, orders)
	_, _ = uc.AssignCourier(context.Background(), order.ID, "liv-1")

	require.NoError(t, uc.UnassignCourier(context.Background(), order.ID, "liv-1"))

	_, err := uc.AssignCourier(context.Background(), order.ID, "liv-2")
	require.NoError(t, err)
}

func TestUpdateStatusForCourierAppliesCourierRules(t *testing.T) {
	uc, orders, _, _ := setup()
	order := readyOrder(t, uc, orders)
	_, _ = uc.AssignCourier(context.Background(), order.ID, "liv-1")

	_, err := uc.UpdateStatusForCourier(context.Background(), order.ID, "liv-2", domain.StatusInDelivery)
	require.Error(t, err, "another courier cannot pick it up")

	_, err = uc.UpdateStatusForCourier(context.Background(), order.ID, "liv-1", domain.StatusCancelled)
	require.Error(t, err, "a courier cannot cancel")

	updated, err := uc.UpdateStatusForCourier(context.Background(), order.ID, "liv-1", domain.StatusInDelivery)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInDelivery, updated.Status)
}
