package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// EventOrderReadyForPickup is the routing key delivery-service creates a
// delivery from. Every lifecycle change publishes "order.<status>"
// (order.confirmed, order.in_delivery, order.cancelled...), so consumers bind
// "order.*" or the exact keys they need.
const EventOrderReadyForPickup = "order.ready_for_pickup"

// EventTypeFor returns the routing key of a transition to status.
func EventTypeFor(status OrderStatus) string {
	return "order." + strings.ToLower(string(status))
}

// Event is a domain event, written to the outbox in the same transaction as
// the change it describes and relayed to the broker afterwards: it exists if
// and only if the change was committed (at-least-once delivery).
type Event struct {
	ID         string
	Type       string
	OccurredAt time.Time
	Data       OrderEventData
}

// OrderEventData carries what consumers need so they never have to call back
// order-service (delivery creation, customer notifications, analytics).
type OrderEventData struct {
	OrderID         string  `json:"orderId"`
	CustomerID      string  `json:"customerId"`
	RestaurantID    string  `json:"restaurantId"`
	Status          string  `json:"status"`
	DeliveryAddress string  `json:"deliveryAddress"`
	AmountDueCents  int64   `json:"amountDueCents"`
	CourierID       *string `json:"courierId,omitempty"`
}

// NewOrderEvent snapshots the order after a transition.
func NewOrderEvent(o *Order) *Event {
	return &Event{
		ID:         uuid.NewString(),
		Type:       EventTypeFor(o.Status),
		OccurredAt: time.Now().UTC(),
		Data: OrderEventData{
			OrderID:         o.ID,
			CustomerID:      o.CustomerID,
			RestaurantID:    o.RestaurantID,
			Status:          string(o.Status),
			DeliveryAddress: o.DeliveryAddress,
			AmountDueCents:  o.AmountDueCents(),
			CourierID:       o.CourierID,
		},
	}
}
