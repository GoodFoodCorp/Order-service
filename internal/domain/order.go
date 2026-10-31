package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
	StatusReadyForPickup: {StatusInDelivery, StatusDelivered, StatusCancelled},
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

// Limits of one order: a typing slip or a script must not tie up a kitchen
// (or its stock) with a thousand burgers; larger orders go through the
// restaurant directly.
const (
	MaxItemQuantity       = 50
	MaxOrderLines         = 50
	MaxInstructionsLength = 300
)

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
	// Fulfillment says whether a courier delivers the order or the customer
	// collects it at the counter (click & collect).
	Fulfillment Fulfillment
	// DeliveryFeeCents is the restaurant's fee, 0 for a pickup.
	DeliveryFeeCents int64
	// TipCents goes to the courier (or the kitchen for a pickup).
	TipCents int64
	// EstimatedMinutes is the delivery (or pickup) time quoted at checkout.
	EstimatedMinutes int
	// DropoffLat/DropoffLng locate the delivery address when it was geocoded.
	DropoffLat *float64
	DropoffLng *float64
	// Cancellation says who cancelled the order and why (zero otherwise).
	Cancellation Cancellation
}

// Fulfillment is how the order reaches the customer.
type Fulfillment string

const (
	FulfillmentDelivery Fulfillment = "DELIVERY"
	FulfillmentPickup   Fulfillment = "PICKUP"
)

// ParseFulfillment defaults to DELIVERY.
func ParseFulfillment(s string) (Fulfillment, error) {
	switch f := Fulfillment(strings.ToUpper(strings.TrimSpace(s))); f {
	case "":
		return FulfillmentDelivery, nil
	case FulfillmentDelivery, FulfillmentPickup:
		return f, nil
	default:
		return "", NewValidationError("fulfillment must be DELIVERY or PICKUP")
	}
}

// IsPickup reports whether the customer collects the order themselves.
func (o *Order) IsPickup() bool { return o.Fulfillment == FulfillmentPickup }

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
	return NewOrderFor(OrderSpec{CustomerID: customerID, RestaurantID: restaurantID,
		Fulfillment: FulfillmentDelivery, DeliveryAddress: deliveryAddress, Items: items})
}

// OrderSpec describes an order to create.
type OrderSpec struct {
	CustomerID      string
	RestaurantID    string
	Fulfillment     Fulfillment
	DeliveryAddress string
	Items           []NewOrderItemInput
}

// NewOrderFor builds a valid PLACED order. A delivery needs an address; a
// pickup does not.
func NewOrderFor(spec OrderSpec) (*Order, error) {
	customerID, restaurantID, deliveryAddress, items := spec.CustomerID, spec.RestaurantID, spec.DeliveryAddress, spec.Items
	if spec.Fulfillment == "" {
		spec.Fulfillment = FulfillmentDelivery
	}
	if customerID == "" {
		return nil, NewValidationError("customer id is required")
	}
	if restaurantID == "" {
		return nil, NewValidationError("restaurant id is required")
	}
	if spec.Fulfillment == FulfillmentDelivery && strings.TrimSpace(deliveryAddress) == "" {
		return nil, NewValidationError("delivery address is required")
	}
	if len(items) == 0 {
		return nil, NewValidationError("an order requires at least one item")
	}
	if len(items) > MaxOrderLines {
		return nil, NewValidationError(fmt.Sprintf("an order holds at most %d different dishes", MaxOrderLines))
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
		if in.Quantity > MaxItemQuantity {
			return nil, NewValidationError(fmt.Sprintf("at most %d of one dish per order", MaxItemQuantity))
		}
		if utf8.RuneCountInString(in.SpecialInstructions) > MaxInstructionsLength {
			return nil, NewValidationError(fmt.Sprintf("special instructions are limited to %d characters", MaxInstructionsLength))
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
		Fulfillment:      spec.Fulfillment,
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
// From READY_FOR_PICKUP a delivery goes IN_DELIVERY with its courier, while a
// pickup goes straight to DELIVERED when the customer collects it.
func (o *Order) TransitionTo(target OrderStatus) error {
	if !o.CanTransitionTo(target) {
		return NewInvalidTransitionError(o.Status, target)
	}
	if o.Status == StatusReadyForPickup &&
		((target == StatusDelivered && !o.IsPickup()) || (target == StatusInDelivery && o.IsPickup())) {
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
// promo discount, plus the delivery fee and the tip. TotalAmountCents itself
// always stays the pre-discount subtotal so a code's history stays traceable.
func (o *Order) AmountDueCents() int64 {
	return o.TotalAmountCents - o.DiscountCents + o.DeliveryFeeCents + o.TipCents
}

// MaxTipCents caps a tip, against typos (100 € instead of 1 €).
const MaxTipCents = 10_000

// SetTip records the customer's tip.
func (o *Order) SetTip(cents int64) error {
	if cents < 0 || cents > MaxTipCents {
		return NewValidationError("tip must be between 0 and 100 €")
	}
	o.TipCents = cents
	return nil
}

// ApplyQuote records the restaurant's answer for this order: delivery fee
// (none for a pickup), estimated time and drop-off position. The minimum
// order applies to deliveries, on the discounted subtotal.
func (o *Order) ApplyQuote(q RestaurantQuote) error {
	if !q.CanOrder {
		return NewValidationError(q.RefusalMessage())
	}
	o.EstimatedMinutes = q.EstimatedMinutes
	if o.IsPickup() {
		o.DeliveryFeeCents = 0
		o.EstimatedMinutes = q.PrepTimeMinutes
		return nil
	}
	if subtotal := o.TotalAmountCents - o.DiscountCents; subtotal < q.MinimumOrderCents {
		return NewValidationError(fmt.Sprintf("minimum order for delivery: %d,%02d €", q.MinimumOrderCents/100, q.MinimumOrderCents%100))
	}
	o.DeliveryFeeCents = q.DeliveryFeeCents
	o.DropoffLat, o.DropoffLng = q.DropoffLat, q.DropoffLng
	return nil
}

// RestaurantQuote is franchise-service's answer to "can this restaurant take
// this order now, and at what delivery fee".
type RestaurantQuote struct {
	CanOrder          bool
	Refusal           string
	DeliveryFeeCents  int64
	MinimumOrderCents int64
	PrepTimeMinutes   int
	EstimatedMinutes  int
	DropoffLat        *float64
	DropoffLng        *float64
}

// RefusalMessage is customer-facing.
func (q RestaurantQuote) RefusalMessage() string {
	switch q.Refusal {
	case "CLOSED":
		return "Le restaurant est fermé pour le moment."
	case "PAUSED":
		return "Le restaurant ne prend plus de commandes pour le moment, réessayez plus tard."
	case "OUTOFZONE":
		return "Cette adresse est hors de la zone de livraison du restaurant."
	case "UNKNOWNADDRESS":
		return "Adresse de livraison introuvable, vérifiez-la."
	case "INACTIVE":
		return "Ce restaurant n'est plus disponible."
	default:
		return "Ce restaurant ne peut pas prendre votre commande pour le moment."
	}
}

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
func (o *Order) IsClaimableByCourier() bool { return !o.IsPickup() && claimableByCourier[o.Status] }

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
