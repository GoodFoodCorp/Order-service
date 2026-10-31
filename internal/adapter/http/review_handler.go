package http

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"goodfood/order-service/internal/domain"
)

type reviewRequest struct {
	Rating  int    `json:"rating"`
	Comment string `json:"comment"`
}

// reviewResponse never names the customer: reviews are shown as "verified
// customer" with the dates the law asks for.
type reviewResponse struct {
	ID        string    `json:"id"`
	Rating    int       `json:"rating"`
	Comment   string    `json:"comment"`
	OrderedAt time.Time `json:"ordered_at"`
	CreatedAt time.Time `json:"created_at"`
}

type reviewSummaryResponse struct {
	RestaurantID string  `json:"restaurant_id"`
	Count        int     `json:"count"`
	Average      float64 `json:"average"`
	Stars        [5]int  `json:"stars"`
}

type restaurantReviewsResponse struct {
	reviewSummaryResponse
	Items []reviewResponse `json:"items"`
}

func toReviewResponse(r *domain.Review) reviewResponse {
	return reviewResponse{ID: r.OrderID, Rating: r.Rating, Comment: r.Comment, OrderedAt: r.OrderedAt, CreatedAt: r.CreatedAt}
}

func toSummaryResponse(s domain.ReviewSummary) reviewSummaryResponse {
	// One decimal, as shown: 4,3 / 5.
	return reviewSummaryResponse{RestaurantID: s.RestaurantID, Count: s.Count, Average: math.Round(s.Average*10) / 10, Stars: s.Stars}
}

// POST /api/orders/{id}/review
func (h *OrderHandler) Review(w http.ResponseWriter, r *http.Request) {
	var req reviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid JSON body")
		return
	}
	review, err := h.uc.ReviewOrder(r.Context(), actorFrom(r), chi.URLParam(r, "id"), req.Rating, req.Comment)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toReviewResponse(review))
}

// GET /api/orders/{id}/review
func (h *OrderHandler) GetReview(w http.ResponseWriter, r *http.Request) {
	review, err := h.uc.OrderReview(r.Context(), actorFrom(r), chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toReviewResponse(review))
}

// GET /api/reviews?restaurantId=&limit=&offset= — public.
func (h *OrderHandler) ListReviews(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	restaurantID := strings.TrimSpace(q.Get("restaurantId"))
	if _, err := uuid.Parse(restaurantID); err != nil {
		writeError(w, r, http.StatusBadRequest, "restaurantId must be a valid UUID")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	offset = max(offset, 0)
	summary, items, err := h.uc.RestaurantReviews(r.Context(), restaurantID, limit, offset)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	out := restaurantReviewsResponse{reviewSummaryResponse: toSummaryResponse(summary), Items: make([]reviewResponse, 0, len(items))}
	for i := range items {
		out.Items = append(out.Items, toReviewResponse(&items[i]))
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, out)
}

// GET /api/reviews/summary?restaurantIds=a,b — public: the marks shown on
// the restaurant list.
func (h *OrderHandler) ReviewSummaries(w http.ResponseWriter, r *http.Request) {
	ids := []string{}
	for _, raw := range strings.Split(r.URL.Query().Get("restaurantIds"), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		if _, err := uuid.Parse(raw); err != nil {
			writeError(w, r, http.StatusBadRequest, "restaurantIds must be valid UUIDs")
			return
		}
		ids = append(ids, raw)
	}
	marks, err := h.uc.ReviewSummaries(r.Context(), ids)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	out := make([]reviewSummaryResponse, 0, len(marks))
	for _, m := range marks {
		out = append(out, toSummaryResponse(m))
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, out)
}

// DELETE /api/reviews/{id} — head office moderation.
func (h *OrderHandler) DeleteReview(w http.ResponseWriter, r *http.Request) {
	if err := h.uc.DeleteReview(r.Context(), actorFrom(r), chi.URLParam(r, "id")); err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
