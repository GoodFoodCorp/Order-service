package domain

import "context"

// OrderRepository is the persistence port implemented by the postgres adapter.
type OrderRepository interface {
	Create(ctx context.Context, order *Order) error
	GetByID(ctx context.Context, id string) (*Order, error)
	ListByCustomer(ctx context.Context, customerID string) ([]Order, error)
	ListByRestaurant(ctx context.Context, restaurantID string) ([]Order, error)
	ListByStatuses(ctx context.Context, statuses ...OrderStatus) ([]Order, error)
	// UpdateStatus writes order.Status (and confirmed_at) only if the persisted
	// status still equals from, the status the caller read — a stale read can
	// never regress or resurrect an order. If the row already holds the target
	// status (a concurrent identical update) it succeeds without writing;
	// otherwise a concurrent change yields a CONFLICT error.
	UpdateStatus(ctx context.Context, order *Order, from OrderStatus) error
	// ConfirmWithStockSync writes the CONFIRMED status, confirmed_at and the
	// stock_sync intent in one UPDATE, so a paid order is never left without
	// a pending consumption. The write only applies while the persisted status
	// is PAYMENT_PENDING (the only predecessor of CONFIRMED); applied=false
	// means another request changed the order first (or it does not exist) and
	// nothing was written — the caller re-reads the order.
	ConfirmWithStockSync(ctx context.Context, order *Order, stockSync StockSyncState) (applied bool, err error)
	// CancelWithStockSync cancels the order if its persisted status still allows
	// it, deciding the stock outcome from the row's own pre-update values (not
	// from the caller's possibly stale read): a paid order (CONFIRMED,
	// IN_PREPARATION, READY_FOR_PICKUP) whose stock_sync is CONSUMED or
	// CONSUME_PENDING moves to RESTORE_PENDING. order.StockSync and
	// order.ConfirmedAt are refreshed from the updated row. A missing order
	// yields NOT_FOUND, a status that no longer allows cancelling a CONFLICT.
	CancelWithStockSync(ctx context.Context, order *Order) error
	// UpdateStockSync is a compare-and-set: stock_sync moves from -> to only if
	// it currently equals from. applied=false (lost race) is not an error.
	UpdateStockSync(ctx context.Context, orderID string, from, to StockSyncState) (applied bool, err error)
	// ListByStockSync returns at most limit orders in one of the given states,
	// oldest first (used by the retry worker).
	ListByStockSync(ctx context.Context, states []StockSyncState, limit int) ([]Order, error)
}

// PaymentIntentRequest asks payment-service to charge an order. The amount
// is always the one this service priced: payment-service has no other source.
type PaymentIntentRequest struct {
	OrderID     string
	CustomerID  string
	AmountCents int64
	Currency    string
	// PaymentMethodID optionally selects one of the customer's saved cards.
	PaymentMethodID string
}

// PaymentIntent is what payment-service returns when an intent is created.
type PaymentIntent struct {
	ID           string
	ClientSecret string
	Status       string
	AmountCents  int64
	Currency     string
}

// PaymentStatusSucceeded is payment-service's status of a captured payment.
const PaymentStatusSucceeded = "SUCCEEDED"

// Payment is payment-service's record of an order's payment.
type Payment struct {
	OrderID     string
	CustomerID  string
	Status      string
	AmountCents int64
	Currency    string
}

// PaymentService is the outbound port to payment-service (internal API,
// service key). Future EBICS integration would be a sibling port, keeping the
// domain provider-agnostic.
type PaymentService interface {
	CreateIntent(ctx context.Context, req PaymentIntentRequest) (*PaymentIntent, error)
	// GetPayment returns the order's payment reconciled with the provider; a
	// missing payment is a NOT_FOUND error.
	GetPayment(ctx context.Context, orderID string) (*Payment, error)
	// Refund pays a cancelled order back, or closes its unpaid intent.
	// Idempotent; a missing payment is a NOT_FOUND error.
	Refund(ctx context.Context, orderID string) error
}

// PromoPreview is what promo-service returns for a code that is currently
// eligible for the given order amount.
type PromoPreview struct {
	Code          string
	DiscountCents int64
}

// PromoService is the outbound port to promo-service, which owns promo codes
// and their redemptions.
type PromoService interface {
	// Preview validates a code without consuming it — safe to call repeatedly
	// while the customer is still assembling their order. The customer's JWT
	// is forwarded.
	Preview(ctx context.Context, token, code string, orderAmountCents int64) (*PromoPreview, error)
	// Redeem atomically consumes one usage of the code for this paid order
	// (internal API). Idempotent per orderID: retries never double-consume.
	Redeem(ctx context.Context, code, orderID, customerID string, orderAmountCents int64) (int64, error)
	// Release gives back the usage of a cancelled, refunded order. Idempotent.
	Release(ctx context.Context, orderID string) error
}

// MenuCatalog is the price authority. What the browser sends is never trusted
// for money: before an order is created, its lines go through the catalog.
type MenuCatalog interface {
	// PriceItems returns the same lines (same dishes, same quantities) carrying
	// the catalog's own name and unit price for this restaurant. A dish that
	// cannot be ordered there (unknown, unavailable, hidden) is a validation
	// error naming it; any other error means the prices could not be verified.
	PriceItems(ctx context.Context, restaurantID string, items []OrderItem) ([]OrderItem, error)
}
