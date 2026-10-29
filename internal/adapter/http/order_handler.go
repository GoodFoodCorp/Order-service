package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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
		PromoCode:       req.PromoCode,
		IdempotencyKey:  r.Header.Get("Idempotency-Key"),
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

// GET /api/orders?customerId=|restaurantId=&status=A,B&limit=&offset=
//
// Most recent first, 50 per page by default (200 max). A full page carries an
// X-Next-Offset header pointing at the next one.
func (h *OrderHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter, err := listFilterFrom(q.Get("limit"), q.Get("offset"), q.Get("status"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	var orders []domain.Order
	if restaurantID := q.Get("restaurantId"); restaurantID != "" {
		orders, err = h.uc.ListRestaurantOrders(r.Context(), actorFrom(r), restaurantID, filter)
	} else {
		orders, err = h.uc.ListCustomerOrders(r.Context(), actorFrom(r), q.Get("customerId"), filter)
	}
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if len(orders) == filter.Limit {
		w.Header().Set("X-Next-Offset", strconv.Itoa(filter.Offset+filter.Limit))
	}
	writeOrders(w, orders)
}

func listFilterFrom(limit, offset, statuses string) (domain.ListFilter, error) {
	l, _ := strconv.Atoi(limit)
	o, _ := strconv.Atoi(offset)
	var parsed []domain.OrderStatus
	for _, raw := range strings.Split(statuses, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		s, err := domain.ParseOrderStatus(raw)
		if err != nil {
			return domain.ListFilter{}, err
		}
		parsed = append(parsed, s)
	}
	return domain.NewListFilter(l, o, parsed), nil
}

// POST /api/orders/{id}/payment-intent  [{payment_method_id}]
func (h *OrderHandler) CreatePaymentIntent(w http.ResponseWriter, r *http.Request) {
	var req paymentIntentRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	res, err := h.uc.CreatePaymentIntent(r.Context(), actorFrom(r), chi.URLParam(r, "id"), req.PaymentMethodID)
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

// POST /api/orders/{id}/cancel  (the customer cancels their own order)
func (h *OrderHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	order, err := h.uc.CancelMyOrder(r.Context(), actorFrom(r), chi.URLParam(r, "id"))
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
