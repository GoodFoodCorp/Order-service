//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goodfood/order-service/internal/domain"
)

func TestReviewRepository(t *testing.T) {
	ctx := context.Background()
	orders := setupRepo(t)
	reviews := NewReviewRepository(orders.pool)

	first := newStoredOrder(t, orders, domain.StatusDelivered)
	second := newStoredOrder(t, orders, domain.StatusDelivered)
	second.RestaurantID = first.RestaurantID
	_, err := orders.pool.Exec(ctx, `UPDATE orders SET restaurant_id = $1 WHERE id = $2`, first.RestaurantID, second.ID)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, rv := range []domain.Review{
		{OrderID: first.ID, RestaurantID: first.RestaurantID, CustomerID: first.CustomerID, Rating: 5, Comment: "Parfait", OrderedAt: first.PlacedAt, CreatedAt: now.Add(-time.Minute)},
		{OrderID: second.ID, RestaurantID: first.RestaurantID, CustomerID: second.CustomerID, Rating: 2, Comment: "Froid", OrderedAt: second.PlacedAt, CreatedAt: now},
	} {
		require.NoError(t, reviews.Create(ctx, &rv))
	}
	dup := domain.Review{OrderID: first.ID, RestaurantID: first.RestaurantID, CustomerID: first.CustomerID, Rating: 1, OrderedAt: now, CreatedAt: now}
	var derr *domain.Error
	require.ErrorAs(t, reviews.Create(ctx, &dup), &derr)
	assert.Equal(t, domain.ErrCodeConflict, derr.Code)

	got, err := reviews.ByOrder(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "Parfait", got.Comment)
	_, err = reviews.ByOrder(ctx, "not-a-uuid")
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeNotFound, derr.Code)

	page, err := reviews.ListByRestaurant(ctx, first.RestaurantID, 10, 0)
	require.NoError(t, err)
	require.Len(t, page, 2)
	assert.Equal(t, "Froid", page[0].Comment, "newest first")

	marks, err := reviews.Summaries(ctx, []string{first.RestaurantID, newStoredOrder(t, orders, domain.StatusPlaced).RestaurantID})
	require.NoError(t, err)
	require.Len(t, marks, 1, "a restaurant without reviews has no mark")
	assert.Equal(t, 2, marks[0].Count)
	assert.InDelta(t, 3.5, marks[0].Average, 0.001)
	assert.Equal(t, [5]int{0, 1, 0, 0, 1}, marks[0].Stars)

	require.NoError(t, reviews.Delete(ctx, second.ID))
	require.ErrorAs(t, reviews.Delete(ctx, second.ID), &derr)
	assert.Equal(t, domain.ErrCodeNotFound, derr.Code)

	_, err = orders.GetByID(ctx, "1; DROP TABLE orders")
	require.ErrorAs(t, err, &derr)
	assert.Equal(t, domain.ErrCodeNotFound, derr.Code)
}
