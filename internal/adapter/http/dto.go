package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"goodfood/order-service/internal/domain"
)

// ── Requests ────────────────────────────────────────────────

type createOrderItemRequest struct {
	MenuItemID          string `json:"menu_item_id"`
	MenuItemName        string `json:"menu_item_name"`
	Quantity            int    `json:"quantity"`
	UnitPriceCents      int64  `json:"unit_price_cents"`
	SpecialInstructions string `json:"special_instructions"`
}

type createOrderRequest struct {
	RestaurantID    string                   `json:"restaurant_id"`
	DeliveryAddress string                   `json:"delivery_address"`
	Items           []createOrderItemRequest `json:"items"`
	PromoCode       string                   `json:"promo_code"`
	Fulfillment     string                   `json:"fulfillment"`
	TipCents        int64                    `json:"tip_cents"`
}

type updateStatusRequest struct {
	Status string `json:"status"`
}

// courierRequest identifies the courier delivery-service acts for.
type courierRequest struct {
	CourierID string `json:"courier_id"`
}

// courierStatusRequest is a delivery transition made on behalf of a courier.
type courierStatusRequest struct {
	Status    string `json:"status"`
	CourierID string `json:"courier_id"`
}

// paymentIntentRequest optionally selects one of the customer's saved cards.
// It deliberately has no amount: the order decides what is charged.
type paymentIntentRequest struct {
	PaymentMethodID string `json:"payment_method_id"`
}

// maxBodyBytes bounds every JSON body this service reads.
const maxBodyBytes = 1 << 20

// ── Responses ───────────────────────────────────────────────

type orderItemResponse struct {
	ID                  string `json:"id"`
	MenuItemID          string `json:"menu_item_id"`
	MenuItemName        string `json:"menu_item_name"`
	Quantity            int    `json:"quantity"`
	UnitPriceCents      int64  `json:"unit_price_cents"`
	SpecialInstructions string `json:"special_instructions,omitempty"`
}

type orderResponse struct {
	ID               string              `json:"id"`
	CustomerID       string              `json:"customer_id"`
	RestaurantID     string              `json:"restaurant_id"`
	Status           string              `json:"status"`
	TotalAmountCents int64               `json:"total_amount_cents"`
	DiscountCents    int64               `json:"discount_cents,omitempty"`
	AmountDueCents   int64               `json:"amount_due_cents"`
	PromoCode        *string             `json:"promo_code,omitempty"`
	DeliveryAddress  string              `json:"delivery_address"`
	Items            []orderItemResponse `json:"items"`
	PlacedAt         time.Time           `json:"placed_at"`
	ConfirmedAt      *time.Time          `json:"confirmed_at,omitempty"`
	CourierID        *string             `json:"courier_id,omitempty"`
	Fulfillment      string              `json:"fulfillment"`
	DeliveryFeeCents int64               `json:"delivery_fee_cents"`
	TipCents         int64               `json:"tip_cents"`
	EstimatedMinutes int                 `json:"estimated_minutes,omitempty"`
	DropoffLat       *float64            `json:"dropoff_lat,omitempty"`
	DropoffLng       *float64            `json:"dropoff_lng,omitempty"`
}

type paymentIntentResponse struct {
	IntentID     string `json:"intent_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	AmountCents  int64  `json:"amount_cents"`
	Currency     string `json:"currency"`
}

type errorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

func toOrderResponse(o *domain.Order) orderResponse {
	items := make([]orderItemResponse, 0, len(o.Items))
	for _, it := range o.Items {
		items = append(items, orderItemResponse{
			ID:                  it.ID,
			MenuItemID:          it.MenuItemID,
			MenuItemName:        it.MenuItemName,
			Quantity:            it.Quantity,
			UnitPriceCents:      it.UnitPriceCents,
			SpecialInstructions: it.SpecialInstructions,
		})
	}
	return orderResponse{
		ID:               o.ID,
		CustomerID:       o.CustomerID,
		RestaurantID:     o.RestaurantID,
		Status:           string(o.Status),
		TotalAmountCents: o.TotalAmountCents,
		DiscountCents:    o.DiscountCents,
		AmountDueCents:   o.AmountDueCents(),
		PromoCode:        o.PromoCode,
		DeliveryAddress:  o.DeliveryAddress,
		Items:            items,
		PlacedAt:         o.PlacedAt,
		ConfirmedAt:      o.ConfirmedAt,
		CourierID:        o.CourierID,
		Fulfillment:      string(o.Fulfillment),
		DeliveryFeeCents: o.DeliveryFeeCents,
		TipCents:         o.TipCents,
		EstimatedMinutes: o.EstimatedMinutes,
		DropoffLat:       o.DropoffLat,
		DropoffLng:       o.DropoffLng,
	}
}

// ── JSON helpers ────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	reqID, _ := r.Context().Value(ctxKeyRequestID).(string)
	writeJSON(w, status, errorResponse{Error: msg, RequestID: reqID})
}

// writeDomainError maps typed business errors to HTTP status codes.
// This mapping lives only in the adapter layer.
func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	var derr *domain.Error
	if errors.As(err, &derr) {
		status := map[domain.ErrorCode]int{
			domain.ErrCodeValidation:        http.StatusBadRequest,
			domain.ErrCodeNotFound:          http.StatusNotFound,
			domain.ErrCodeForbidden:         http.StatusForbidden,
			domain.ErrCodeInvalidTransition: http.StatusConflict,
			domain.ErrCodeConflict:          http.StatusConflict,
			domain.ErrCodePayment:           http.StatusBadGateway,
			domain.ErrCodeOutOfStock:        http.StatusConflict,
			domain.ErrCodeUnavailable:       http.StatusServiceUnavailable,
		}[derr.Code]
		if status == 0 {
			status = http.StatusInternalServerError
		}
		writeError(w, r, status, derr.Message)
		return
	}
	writeError(w, r, http.StatusInternalServerError, "internal server error")
}
