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

// ── Test fixtures ───────────────────────────────────────────

var (
	customer  = Actor{UserID: "cust-1", RoleSlugs: []string{RoleUser}}
	otherCust = Actor{UserID: "cust-2", RoleSlugs: []string{RoleUser}}
	manager   = Actor{UserID: "mgr-1", TenantID: "resto-1", RoleSlugs: []string{RoleManager}}
	admin     = Actor{UserID: "adm-1", RoleSlugs: []string{RoleAdmin}}
	courier   = Actor{UserID: "liv-1", RoleSlugs: []string{RoleCourier}}
)

func setup() (*UseCases, *fakeOrderRepo, *fakePayments) {
	orders := newFakeOrderRepo()
	payments := newFakePayments()
	return NewUseCases(orders, payments), orders, payments
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
	uc, orders, _ := setup()

	order := placeTestOrder(t, uc)
	assert.Equal(t, domain.StatusPlaced, order.Status)
	assert.Equal(t, int64(2598), order.TotalAmountCents)
	assert.Equal(t, customer.UserID, order.CustomerID)
	assert.Len(t, orders.orders, 1)
}

func TestPlaceOrderRequiresCustomerRole(t *testing.T) {
	uc, _, _ := setup()

	for _, actor := range []Actor{manager, courier, {UserID: "x"}} {
		_, err := uc.PlaceOrder(context.Background(), actor, PlaceOrderInput{})
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
	}
}

// ── GetOrder ────────────────────────────────────────────────

func TestGetOrderAccessRules(t *testing.T) {
	uc, _, _ := setup()
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
	uc, _, _ := setup()
	_, err := uc.GetOrder(context.Background(), customer, "missing")
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeNotFound, derr.Code)
}

// ── ListCustomerOrders ──────────────────────────────────────

func TestListCustomerOrders(t *testing.T) {
	uc, _, _ := setup()
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
	uc, _, _ := setup()
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
	uc, orders, payments := setup()
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
	uc, _, _ := setup()
	order := placeTestOrder(t, uc)

	_, err := uc.CreatePaymentIntent(context.Background(), otherCust, order.ID)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
}

func TestCreatePaymentIntentWrongState(t *testing.T) {
	uc, orders, _ := setup()
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
	uc, orders, _ := setup()
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

func TestConfirmOrderPaymentNotCompleted(t *testing.T) {
	uc, _, payments := setup()
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
	uc, _, _ := setup()
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
	uc, _, _ := setup()
	order := confirmOrder(t, uc)

	updated, err := uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusInPreparation)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInPreparation, updated.Status)

	updated, err = uc.UpdateOrderStatus(context.Background(), manager, order.ID, domain.StatusReadyForPickup)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusReadyForPickup, updated.Status)
}

func TestUpdateOrderStatusManagerWrongTenant(t *testing.T) {
	uc, _, _ := setup()
	order := confirmOrder(t, uc)

	wrongManager := Actor{UserID: "mgr-2", TenantID: "resto-99", RoleSlugs: []string{RoleManager}}
	_, err := uc.UpdateOrderStatus(context.Background(), wrongManager, order.ID, domain.StatusInPreparation)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
}

func TestUpdateOrderStatusCourierLimits(t *testing.T) {
	uc, _, _ := setup()
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
	uc, _, _ := setup()
	order := confirmOrder(t, uc)

	_, err := uc.UpdateOrderStatus(context.Background(), admin, order.ID, domain.StatusDelivered)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeInvalidTransition, derr.Code)
}

func TestUpdateOrderStatusCustomerForbidden(t *testing.T) {
	uc, _, _ := setup()
	order := confirmOrder(t, uc)

	_, err := uc.UpdateOrderStatus(context.Background(), customer, order.ID, domain.StatusInPreparation)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeForbidden, derr.Code)
}

// ── ListReadyForDelivery ────────────────────────────────────

func TestListReadyForDelivery(t *testing.T) {
	uc, orders, _ := setup()
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
