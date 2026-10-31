package application

import (
	"context"
	"time"

	"goodfood/order-service/internal/domain"
)

// MaxReviewSummaries bounds one marks request (a page of restaurants).
const MaxReviewSummaries = 100

// WithReviews turns reviews on.
func (uc *UseCases) WithReviews(repo domain.ReviewRepository) *UseCases {
	uc.reviews = repo
	return uc
}

func (uc *UseCases) reviewRepo() (domain.ReviewRepository, error) {
	if uc.reviews == nil {
		return nil, domain.NewUnavailableError("reviews are not available")
	}
	return uc.reviews, nil
}

// ReviewOrder records the customer's review of their received order.
func (uc *UseCases) ReviewOrder(ctx context.Context, actor Actor, orderID string, rating int, comment string) (*domain.Review, error) {
	repo, err := uc.reviewRepo()
	if err != nil {
		return nil, err
	}
	order, err := uc.GetOrder(ctx, actor, orderID)
	if err != nil {
		return nil, err
	}
	if !order.IsOwnedBy(actor.UserID) {
		return nil, domain.NewForbiddenError("only the customer can review this order")
	}
	review, err := domain.NewReview(order, rating, comment, time.Now())
	if err != nil {
		return nil, err
	}
	if err := repo.Create(ctx, review); err != nil {
		return nil, err
	}
	return review, nil
}

// OrderReview returns the review of an order to whoever may see the order.
func (uc *UseCases) OrderReview(ctx context.Context, actor Actor, orderID string) (*domain.Review, error) {
	repo, err := uc.reviewRepo()
	if err != nil {
		return nil, err
	}
	if _, err := uc.GetOrder(ctx, actor, orderID); err != nil {
		return nil, err
	}
	return repo.ByOrder(ctx, orderID)
}

// RestaurantReviews is a restaurant's mark and a page of its reviews (public).
func (uc *UseCases) RestaurantReviews(ctx context.Context, restaurantID string, limit, offset int) (domain.ReviewSummary, []domain.Review, error) {
	summary := domain.ReviewSummary{RestaurantID: restaurantID}
	repo, err := uc.reviewRepo()
	if err != nil {
		return summary, nil, err
	}
	marks, err := repo.Summaries(ctx, []string{restaurantID})
	if err != nil {
		return summary, nil, err
	}
	if len(marks) == 1 {
		summary = marks[0]
	}
	items, err := repo.ListByRestaurant(ctx, restaurantID, limit, offset)
	return summary, items, err
}

// ReviewSummaries returns the marks of several restaurants (public).
func (uc *UseCases) ReviewSummaries(ctx context.Context, restaurantIDs []string) ([]domain.ReviewSummary, error) {
	repo, err := uc.reviewRepo()
	if err != nil {
		return nil, err
	}
	if len(restaurantIDs) > MaxReviewSummaries {
		return nil, domain.NewValidationError("too many restaurants in one request")
	}
	if len(restaurantIDs) == 0 {
		return []domain.ReviewSummary{}, nil
	}
	return repo.Summaries(ctx, restaurantIDs)
}

// DeleteReview is head office's moderation of an unlawful review (insults,
// personal data, off-topic), as the site's terms announce.
func (uc *UseCases) DeleteReview(ctx context.Context, actor Actor, orderID string) error {
	repo, err := uc.reviewRepo()
	if err != nil {
		return err
	}
	if !actor.HasRole(RoleAdmin) {
		return domain.NewForbiddenError("only head office can remove a review")
	}
	return repo.Delete(ctx, orderID)
}
