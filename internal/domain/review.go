package domain

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Reviews are verified: only the customer of a received order may leave one,
// once, within ReviewWindow of ordering — what the Code de la consommation
// (art. L111-7-2, D111-16 s.) asks a platform to say about the reviews it
// shows, along with their date and the date of the order.
const (
	ReviewWindow           = 30 * 24 * time.Hour
	MaxReviewCommentLength = 500
)

type Review struct {
	OrderID      string
	RestaurantID string
	CustomerID   string
	Rating       int
	Comment      string
	OrderedAt    time.Time
	CreatedAt    time.Time
}

// ReviewSummary is a restaurant's mark: the average of its reviews.
type ReviewSummary struct {
	RestaurantID string
	Count        int
	Average      float64
	// Stars[i] counts the reviews of i+1 stars.
	Stars [5]int
}

// NewReview checks the customer may review this order now.
func NewReview(order *Order, rating int, comment string, now time.Time) (*Review, error) {
	if rating < 1 || rating > 5 {
		return nil, NewValidationError("rating must be from 1 to 5")
	}
	comment = strings.TrimSpace(comment)
	if utf8.RuneCountInString(comment) > MaxReviewCommentLength {
		return nil, NewValidationError(fmt.Sprintf("the comment is limited to %d characters", MaxReviewCommentLength))
	}
	if order.Status != StatusDelivered {
		return nil, NewConflictError("only a delivered order can be reviewed")
	}
	if now.Sub(order.PlacedAt) > ReviewWindow {
		return nil, NewConflictError(fmt.Sprintf("reviews are open for %d days after the order", int(ReviewWindow.Hours()/24)))
	}
	return &Review{
		OrderID: order.ID, RestaurantID: order.RestaurantID, CustomerID: order.CustomerID,
		Rating: rating, Comment: comment, OrderedAt: order.PlacedAt, CreatedAt: now.UTC(),
	}, nil
}

// ReviewRepository keeps the reviews.
type ReviewRepository interface {
	// Create stores the review; the order already having one is a CONFLICT.
	Create(ctx context.Context, review *Review) error
	// ByOrder returns the order's review; none is a NOT_FOUND error.
	ByOrder(ctx context.Context, orderID string) (*Review, error)
	// ListByRestaurant returns a page of the restaurant's reviews, newest first.
	ListByRestaurant(ctx context.Context, restaurantID string, limit, offset int) ([]Review, error)
	// Summaries returns the mark of each restaurant that has reviews.
	Summaries(ctx context.Context, restaurantIDs []string) ([]ReviewSummary, error)
	// Delete removes a review (head office moderation); none is NOT_FOUND.
	Delete(ctx context.Context, orderID string) error
}
