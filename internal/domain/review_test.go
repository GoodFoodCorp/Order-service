package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deliveredOrder(placed time.Time) *Order {
	return &Order{ID: "o1", CustomerID: "c1", RestaurantID: "r1", Status: StatusDelivered, PlacedAt: placed}
}

func TestNewReview(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

	r, err := NewReview(deliveredOrder(now.Add(-time.Hour)), 4, "  Très bon  ", now)
	require.NoError(t, err)
	assert.Equal(t, "Très bon", r.Comment)
	assert.Equal(t, "r1", r.RestaurantID)
	assert.Equal(t, now.Add(-time.Hour), r.OrderedAt)

	for _, rating := range []int{0, 6, -1} {
		_, err = NewReview(deliveredOrder(now), rating, "", now)
		assert.Error(t, err, "rating %d", rating)
	}

	_, err = NewReview(deliveredOrder(now), 5, strings.Repeat("é", MaxReviewCommentLength+1), now)
	assert.Error(t, err, "comment too long")
	_, err = NewReview(deliveredOrder(now), 5, strings.Repeat("é", MaxReviewCommentLength), now)
	assert.NoError(t, err, "500 accented characters are fine")

	inKitchen := deliveredOrder(now)
	inKitchen.Status = StatusInPreparation
	_, err = NewReview(inKitchen, 5, "", now)
	assert.Error(t, err, "not received yet")

	_, err = NewReview(deliveredOrder(now.Add(-ReviewWindow-time.Minute)), 5, "", now)
	assert.Error(t, err, "too late")
}
