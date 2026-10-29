package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"goodfood/order-service/internal/domain"
)

// Internal endpoints for delivery-service (service key). Couriers never call
// them: delivery-service enforces which courier runs which delivery and
// passes the courier id along.

// GET /internal/orders/ready-for-delivery
func (h *OrderHandler) ListClaimableInternal(w http.ResponseWriter, r *http.Request) {
	orders, err := h.uc.ListClaimableOrders(r.Context())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeOrders(w, orders)
}

// GET /internal/orders/{id}
func (h *OrderHandler) GetInternal(w http.ResponseWriter, r *http.Request) {
	order, err := h.uc.GetOrderForDelivery(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderResponse(order))
}

// PUT /internal/orders/{id}/courier  {courier_id}
func (h *OrderHandler) AssignCourierInternal(w http.ResponseWriter, r *http.Request) {
	var req courierRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid JSON body")
		return
	}
	order, err := h.uc.AssignCourier(r.Context(), chi.URLParam(r, "id"), req.CourierID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderResponse(order))
}

// DELETE /internal/orders/{id}/courier/{courierId}
func (h *OrderHandler) UnassignCourierInternal(w http.ResponseWriter, r *http.Request) {
	if err := h.uc.UnassignCourier(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "courierId")); err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PATCH /internal/orders/{id}/status  {status, courier_id}
func (h *OrderHandler) UpdateStatusInternal(w http.ResponseWriter, r *http.Request) {
	var req courierStatusRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid JSON body")
		return
	}
	target, err := domain.ParseOrderStatus(req.Status)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	order, err := h.uc.UpdateStatusForCourier(r.Context(), chi.URLParam(r, "id"), req.CourierID, target)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderResponse(order))
}

func writeOrders(w http.ResponseWriter, orders []domain.Order) {
	out := make([]orderResponse, 0, len(orders))
	for i := range orders {
		out = append(out, toOrderResponse(&orders[i]))
	}
	writeJSON(w, http.StatusOK, out)
}
