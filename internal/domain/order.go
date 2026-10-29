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
	PromoCode        *string
	DiscountCents    int64
	StockSync        StockSyncState
	// CourierID is the courier who claimed the delivery (nil until then).
	CourierID *string
	// RefundSync tracks paying back (or closing the intent of) a cancelled
	// order with payment-service.
	RefundSync RefundSyncState
	// IdempotencyKey, when the client sent one, makes a retried creation
	// return this order instead of placing a second one.
	IdempotencyKey *string
}

type NewOrderItemInput struct {
	MenuItemID          string
	MenuItemName        string
	Quantity            int
	UnitPriceCents      int64
	SpecialInstructions string
}

// NewOrder builds a valid PLACED order and computes its total from the
// prices it is given. Those come from the client, so they are only provisional:
// PlaceOrder passes the order through the MenuCatalog (Reprice) before it is
// persisted, and the catalog's prices are the ones that are charged.
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
		menuItemID, err := uuid.Parse(strings.TrimSpace(in.MenuItemID))
		if err != nil {
			return nil, NewValidationError("menu item id must be a valid UUID")
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
			MenuItemID:          menuItemID.String(), // canonical lowercase, as menu-service keys recipes
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
		StockSync:        StockSyncNone,
		RefundSync:       RefundSyncNone,
	}, nil
}

// Reprice replaces the name and unit price of every line with the catalog's and
// recomputes the subtotal. The lines must be the order's own (same dishes, same
// quantities, same order) — only money and label change, never what was asked
// for. On any mismatch the order is left untouched.
func (o *Order) Reprice(priced []OrderItem) error {
	if len(priced) != len(o.Items) {
		return NewValidationError("priced lines do not match the order")
	}
	var total int64
	for i, p := range priced {
		cur := o.Items[i]
		if p.MenuItemID != cur.MenuItemID || p.Quantity != cur.Quantity {
			return NewValidationError("priced lines do not match the order")
		}
		if p.UnitPriceCents <= 0 || strings.TrimSpace(p.MenuItemName) == "" {
			return NewValidationError("the catalog returned an invalid price for " + cur.MenuItemName)
		}
		total += int64(p.Quantity) * p.UnitPriceCents
	}
	for i, p := range priced {
		o.Items[i].MenuItemName = strings.TrimSpace(p.MenuItemName)
		o.Items[i].UnitPriceCents = p.UnitPriceCents
	}
	o.TotalAmountCents = total
	return nil
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

// AmountDueCents is what actually gets charged: the item subtotal minus any
// promo discount. TotalAmountCents itself always stays the pre-discount
// subtotal so a code's history stays traceable on the order.
func (o *Order) AmountDueCents() int64 { return o.TotalAmountCents - o.DiscountCents }

// ApplyPromo records a discount already validated by promo-service. The code
// itself is never re-validated here — promo-service is the source of truth.
func (o *Order) ApplyPromo(code string, discountCents int64) error {
	if discountCents < 0 || discountCents > o.TotalAmountCents {
		return NewValidationError("invalid discount for this order")
	}
	o.PromoCode = &code
	o.DiscountCents = discountCents
	return nil
}

// claimableByCourier are the statuses in which a courier may take an order
// on: still cooking (they drive to the restaurant meanwhile) or ready.
var claimableByCourier = map[OrderStatus]bool{
	StatusInPreparation:  true,
	StatusReadyForPickup: true,
}

// IsClaimableByCourier reports whether a courier may be assigned now.
func (o *Order) IsClaimableByCourier() bool { return claimableByCourier[o.Status] }

// IsAssignedTo reports whether the given courier runs this order's delivery.
func (o *Order) IsAssignedTo(courierID string) bool {
	return o.CourierID != nil && *o.CourierID == courierID
}

// RefundSyncState tracks where a cancelled order stands with payment-service.
type RefundSyncState string

const (
	RefundSyncNone    RefundSyncState = "NONE"
	RefundSyncPending RefundSyncState = "PENDING"
	RefundSyncDone    RefundSyncState = "DONE"
)

// customerCancellable are the statuses in which customers may cancel their
// own order: until the kitchen starts cooking.
var customerCancellable = map[OrderStatus]bool{
	StatusPlaced:         true,
	StatusPaymentPending: true,
	StatusConfirmed:      true,
}

// IsCancellableByCustomer reports whether the customer may still cancel.
func (o *Order) IsCancellableByCustomer() bool { return customerCancellable[o.Status] }

// ListFilter bounds a list query: a page and, optionally, the statuses kept.
type ListFilter struct {
	Statuses []OrderStatus
	Limit    int
	Offset   int
}

// Page sizes of the list endpoints.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// NewListFilter clamps a requested page to the allowed bounds.
func NewListFilter(limit, offset int, statuses []OrderStatus) ListFilter {
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return ListFilter{Statuses: statuses, Limit: limit, Offset: offset}
}
