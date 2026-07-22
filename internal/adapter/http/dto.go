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
}

type updateStatusRequest struct {
	Status string `json:"status"`
}

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
	DeliveryAddress  string              `json:"delivery_address"`
	Items            []orderItemResponse `json:"items"`
	PlacedAt         time.Time           `json:"placed_at"`
	ConfirmedAt      *time.Time          `json:"confirmed_at,omitempty"`
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
		DeliveryAddress:  o.DeliveryAddress,
		Items:            items,
		PlacedAt:         o.PlacedAt,
		ConfirmedAt:      o.ConfirmedAt,
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
		}[derr.Code]
		if status == 0 {
			status = http.StatusInternalServerError
		}
		writeError(w, r, status, derr.Message)
		return
	}
	writeError(w, r, http.StatusInternalServerError, "internal server error")
}
