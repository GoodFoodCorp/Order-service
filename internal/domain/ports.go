package domain

import "context"

// OrderRepository is the persistence port implemented by the postgres adapter.
type OrderRepository interface {
	Create(ctx context.Context, order *Order) error
	GetByID(ctx context.Context, id string) (*Order, error)
	ListByCustomer(ctx context.Context, customerID string) ([]Order, error)
	ListByRestaurant(ctx context.Context, restaurantID string) ([]Order, error)
	ListByStatus(ctx context.Context, status OrderStatus) ([]Order, error)
	UpdateStatus(ctx context.Context, order *Order) error
}

// PaymentIntent is what payment-service returns when an intent is created.
type PaymentIntent struct {
	ID           string
	ClientSecret string
	Status       string
	AmountCents  int64
	Currency     string
}

// PaymentService is the outbound port to payment-service, which owns Stripe
// and the payment records. The caller's JWT is forwarded on every call.
// Future EBICS integration would be a sibling port, keeping the domain
// provider-agnostic.
type PaymentService interface {
	CreateIntent(ctx context.Context, token, orderID string, amountCents int64, currency string) (*PaymentIntent, error)
	Confirm(ctx context.Context, token, orderID string) (string, error)
}
