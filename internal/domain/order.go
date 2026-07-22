package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

type OrderStatus string

const (
	StatusPlaced         OrderStatus = "PLACED"
	StatusPaymentPending OrderStatus = "PAYMENT_PENDING"
	StatusConfirmed      OrderStatus = "CONFIRMED"
	StatusInPreparation  OrderStatus = "IN_PREPARATION"
	StatusReadyForPickup OrderStatus = "READY_FOR_PICKUP"
	StatusInDelivery     OrderStatus = "IN_DELIVERY"
	StatusDelivered      OrderStatus = "DELIVERED"
	StatusCancelled      OrderStatus = "CANCELLED"
)

// allowedTransitions is the single source of truth for the order lifecycle.
var allowedTransitions = map[OrderStatus][]OrderStatus{
	StatusPlaced:         {StatusPaymentPending, StatusCancelled},
	StatusPaymentPending: {StatusConfirmed, StatusCancelled},
	StatusConfirmed:      {StatusInPreparation, StatusCancelled},
	StatusInPreparation:  {StatusReadyForPickup, StatusCancelled},
	StatusReadyForPickup: {StatusInDelivery, StatusCancelled},
	StatusInDelivery:     {StatusDelivered},
	StatusDelivered:      {},
	StatusCancelled:      {},
}

func ParseOrderStatus(s string) (OrderStatus, error) {
	status := OrderStatus(strings.ToUpper(strings.TrimSpace(s)))
	if _, ok := allowedTransitions[status]; !ok {
		return "", NewValidationError("unknown order status: " + s)
	}
	return status, nil
}

type OrderItem struct {
	ID                  string
	OrderID             string
	MenuItemID          string
	MenuItemName        string
	Quantity            int
	UnitPriceCents      int64
	SpecialInstructions string
}

type Order struct {
	ID               string
	CustomerID       string
	RestaurantID     string
	Status           OrderStatus
	TotalAmountCents int64
	DeliveryAddress  string
	Items            []OrderItem
	PlacedAt         time.Time
	ConfirmedAt      *time.Time
}

type NewOrderItemInput struct {
	MenuItemID          string
	MenuItemName        string
	Quantity            int
	UnitPriceCents      int64
	SpecialInstructions string
}

// NewOrder builds a valid PLACED order and computes its total.
// Prices come from the client for this POC (static catalog in the web app);
// a future menu-service would be the price authority.
func NewOrder(customerID, restaurantID, deliveryAddress string, items []NewOrderItemInput) (*Order, error) {
	if customerID == "" {
		return nil, NewValidationError("customer id is required")
	}
	if restaurantID == "" {
		return nil, NewValidationError("restaurant id is required")
	}
	if strings.TrimSpace(deliveryAddress) == "" {
		return nil, NewValidationError("delivery address is required")
	}
	if len(items) == 0 {
		return nil, NewValidationError("an order requires at least one item")
	}

	orderID := uuid.NewString()
	var total int64
	orderItems := make([]OrderItem, 0, len(items))
	for _, in := range items {
		if in.MenuItemID == "" || strings.TrimSpace(in.MenuItemName) == "" {
			return nil, NewValidationError("each item requires a menu item id and name")
		}
		if in.Quantity <= 0 {
			return nil, NewValidationError("item quantity must be greater than zero")
		}
		if in.UnitPriceCents <= 0 {
			return nil, NewValidationError("item unit price must be greater than zero")
		}
		total += int64(in.Quantity) * in.UnitPriceCents
		orderItems = append(orderItems, OrderItem{
			ID:                  uuid.NewString(),
			OrderID:             orderID,
			MenuItemID:          in.MenuItemID,
			MenuItemName:        strings.TrimSpace(in.MenuItemName),
			Quantity:            in.Quantity,
			UnitPriceCents:      in.UnitPriceCents,
			SpecialInstructions: strings.TrimSpace(in.SpecialInstructions),
		})
	}

	return &Order{
		ID:               orderID,
		CustomerID:       customerID,
		RestaurantID:     restaurantID,
		Status:           StatusPlaced,
		TotalAmountCents: total,
		DeliveryAddress:  strings.TrimSpace(deliveryAddress),
		Items:            orderItems,
		PlacedAt:         time.Now().UTC(),
	}, nil
}

// CanTransitionTo reports whether the lifecycle allows moving to target.
func (o *Order) CanTransitionTo(target OrderStatus) bool {
	for _, allowed := range allowedTransitions[o.Status] {
		if allowed == target {
			return true
		}
	}
	return false
}

// TransitionTo applies a lifecycle change, stamping ConfirmedAt when relevant.
func (o *Order) TransitionTo(target OrderStatus) error {
	if !o.CanTransitionTo(target) {
		return NewInvalidTransitionError(o.Status, target)
	}
	o.Status = target
	if target == StatusConfirmed {
		now := time.Now().UTC()
		o.ConfirmedAt = &now
	}
	return nil
}

// IsOwnedBy reports whether the given user placed this order.
func (o *Order) IsOwnedBy(userID string) bool { return o.CustomerID == userID }
