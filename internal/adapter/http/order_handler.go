package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"goodfood/order-service/internal/application"
	"goodfood/order-service/internal/domain"
)

// OrderHandler holds the HTTP endpoints. Handlers only decode/validate DTOs,
// call the application layer and encode responses — no business logic here.
type OrderHandler struct {
	uc *application.UseCases
}

func NewOrderHandler(uc *application.UseCases) *OrderHandler {
	return &OrderHandler{uc: uc}
}

// POST /api/orders
func (h *OrderHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid JSON body")
		return
	}
	items := make([]domain.NewOrderItemInput, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, domain.NewOrderItemInput{
			MenuItemID:          it.MenuItemID,
			MenuItemName:        it.MenuItemName,
			Quantity:            it.Quantity,
			UnitPriceCents:      it.UnitPriceCents,
			SpecialInstructions: it.SpecialInstructions,
		})
	}
	order, err := h.uc.PlaceOrder(r.Context(), actorFrom(r), application.PlaceOrderInput{
		RestaurantID:    req.RestaurantID,
		DeliveryAddress: req.DeliveryAddress,
		Items:           items,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toOrderResponse(order))
}

// GET /api/orders/{id}
func (h *OrderHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	order, err := h.uc.GetOrder(r.Context(), actorFrom(r), chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderResponse(order))
}

// GET /api/orders?customerId=  |  GET /api/orders?restaurantId=
func (h *OrderHandler) List(w http.ResponseWriter, r *http.Request) {
	var (
		orders []domain.Order
		err    error
	)
	if restaurantID := r.URL.Query().Get("restaurantId"); restaurantID != "" {
		orders, err = h.uc.ListRestaurantOrders(r.Context(), actorFrom(r), restaurantID)
	} else {
		orders, err = h.uc.ListCustomerOrders(r.Context(), actorFrom(r), r.URL.Query().Get("customerId"))
	}
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	out := make([]orderResponse, 0, len(orders))
	for i := range orders {
		out = append(out, toOrderResponse(&orders[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/orders/{id}/payment-intent
func (h *OrderHandler) CreatePaymentIntent(w http.ResponseWriter, r *http.Request) {
	res, err := h.uc.CreatePaymentIntent(r.Context(), actorFrom(r), chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, paymentIntentResponse{
		IntentID:     res.IntentID,
		ClientSecret: res.ClientSecret,
		AmountCents:  res.AmountCents,
		Currency:     res.Currency,
	})
}

// POST /api/orders/{id}/confirm
func (h *OrderHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	order, err := h.uc.ConfirmOrder(r.Context(), actorFrom(r), chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderResponse(order))
}

// PATCH /api/orders/{id}/status
func (h *OrderHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	var req updateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid JSON body")
		return
	}
	target, err := domain.ParseOrderStatus(req.Status)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	order, err := h.uc.UpdateOrderStatus(r.Context(), actorFrom(r), chi.URLParam(r, "id"), target)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderResponse(order))
}

// GET /api/orders/ready-for-delivery
func (h *OrderHandler) ListReadyForDelivery(w http.ResponseWriter, r *http.Request) {
	orders, err := h.uc.ListReadyForDelivery(r.Context(), actorFrom(r))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	out := make([]orderResponse, 0, len(orders))
	for i := range orders {
		out = append(out, toOrderResponse(&orders[i]))
	}
	writeJSON(w, http.StatusOK, out)
}
