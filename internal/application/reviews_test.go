package application

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goodfood/order-service/internal/domain"
)

type fakeReviews struct{ byOrder map[string]domain.Review }

func (f *fakeReviews) Create(_ context.Context, r *domain.Review) error {
	if _, ok := f.byOrder[r.OrderID]; ok {
		return domain.NewConflictError("this order has already been reviewed")
	}
	f.byOrder[r.OrderID] = *r
	return nil
}

func (f *fakeReviews) ByOrder(_ context.Context, id string) (*domain.Review, error) {
	r, ok := f.byOrder[id]
	if !ok {
		return nil, domain.NewNotFoundError("this order has no review")
	}
	return &r, nil
}

func (f *fakeReviews) ListByRestaurant(_ context.Context, restaurantID string, _, _ int) ([]domain.Review, error) {
	out := []domain.Review{}
	for _, r := range f.byOrder {
		if r.RestaurantID == restaurantID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeReviews) Summaries(_ context.Context, ids []string) ([]domain.ReviewSummary, error) {
	out := []domain.ReviewSummary{}
	for _, id := range ids {
		s := domain.ReviewSummary{RestaurantID: id}
		sum := 0
		for _, r := range f.byOrder {
			if r.RestaurantID == id {
				s.Count++
				sum += r.Rating
				s.Stars[r.Rating-1]++
			}
		}
		if s.Count > 0 {
			s.Average = float64(sum) / float64(s.Count)
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeReviews) Delete(_ context.Context, id string) error {
	if _, ok := f.byOrder[id]; !ok {
		return domain.NewNotFoundError("this order has no review")
	}
	delete(f.byOrder, id)
	return nil
}

func receivedOrder(t *testing.T, orders *fakeOrderRepo, status domain.OrderStatus) string {
	t.Helper()
	o := &domain.Order{ID: "11111111-1111-4111-8111-00000000000" + string(rune('0'+len(orders.orders))), CustomerID: customer.UserID,
		RestaurantID: "resto-1", Status: status, PlacedAt: time.Now().Add(-time.Hour)}
	require.NoError(t, orders.Create(context.Background(), o))
	return o.ID
}

func TestReviews(t *testing.T) {
	ctx := context.Background()
	uc, orders, _, _ := setup()
	reviews := &fakeReviews{byOrder: map[string]domain.Review{}}
	uc.WithReviews(reviews)

	delivered := receivedOrder(t, orders, domain.StatusDelivered)
	cooking := receivedOrder(t, orders, domain.StatusInPreparation)

	_, err := uc.ReviewOrder(ctx, otherCust, delivered, 5, "faux avis")
	assert.Error(t, err, "another customer cannot review it")
	_, err = uc.ReviewOrder(ctx, manager, delivered, 5, "")
	assert.Error(t, err, "the restaurant cannot review itself")
	_, err = uc.ReviewOrder(ctx, customer, cooking, 5, "")
	assert.Error(t, err, "not received yet")

	r, err := uc.ReviewOrder(ctx, customer, delivered, 4, "Bon")
	require.NoError(t, err)
	assert.Equal(t, "resto-1", r.RestaurantID)
	_, err = uc.ReviewOrder(ctx, customer, delivered, 1, "")
	assert.Error(t, err, "once per order")

	got, err := uc.OrderReview(ctx, manager, delivered)
	require.NoError(t, err)
	assert.Equal(t, 4, got.Rating, "the restaurant sees the review of its order")
	_, err = uc.OrderReview(ctx, otherCust, delivered)
	assert.Error(t, err)

	summary, items, err := uc.RestaurantReviews(ctx, "resto-1", 20, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Count)
	assert.Equal(t, 4.0, summary.Average)
	assert.Len(t, items, 1)

	assert.Error(t, uc.DeleteReview(ctx, manager, delivered), "only head office moderates")
	require.NoError(t, uc.DeleteReview(ctx, admin, delivered))
	marks, err := uc.ReviewSummaries(ctx, []string{"resto-1"})
	require.NoError(t, err)
	assert.Empty(t, marks)
}

func TestReviewsOff(t *testing.T) {
	uc, orders, _, _ := setup()
	_, err := uc.ReviewOrder(context.Background(), customer, receivedOrder(t, orders, domain.StatusDelivered), 5, "")
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeUnavailable, derr.Code)
}
