package domain

import (
	"context"
	"time"
)

// EventOrderReadyForPickup is the routing key of the event published when an
// order becomes READY_FOR_PICKUP.
const EventOrderReadyForPickup = "order.ready_for_pickup"

// OrderReadyEvent carries everything delivery-service needs to create the
// delivery, so consumers never have to call back order-service.
type OrderReadyEvent struct {
	EventID         string
	OrderID         string
	CustomerID      string
	RestaurantID    string
	DeliveryAddress string
	OccurredAt      time.Time
}

// EventPublisher is the outbound messaging port (RabbitMQ adapter).
type EventPublisher interface {
	PublishOrderReady(ctx context.Context, event OrderReadyEvent) error
}

// NoopPublisher is used when no broker is configured (tests, local dev).
type NoopPublisher struct{}

func (NoopPublisher) PublishOrderReady(context.Context, OrderReadyEvent) error { return nil }
