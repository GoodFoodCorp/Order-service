package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"goodfood/order-service/internal/domain"
)

type ReviewRepository struct {
	pool *pgxpool.Pool
}

func NewReviewRepository(pool *pgxpool.Pool) *ReviewRepository {
	return &ReviewRepository{pool: pool}
}

const reviewCols = `order_id, restaurant_id, customer_id, rating, comment, ordered_at, created_at`

func scanReview(row pgx.Row) (*domain.Review, error) {
	var rv domain.Review
	if err := row.Scan(&rv.OrderID, &rv.RestaurantID, &rv.CustomerID, &rv.Rating, &rv.Comment, &rv.OrderedAt, &rv.CreatedAt); err != nil {
		return nil, err
	}
	return &rv, nil
}

func (r *ReviewRepository) Create(ctx context.Context, rv *domain.Review) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO order_reviews (`+reviewCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		rv.OrderID, rv.RestaurantID, rv.CustomerID, rv.Rating, rv.Comment, rv.OrderedAt, rv.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return domain.NewConflictError("this order has already been reviewed")
	}
	return err
}

func (r *ReviewRepository) ByOrder(ctx context.Context, orderID string) (*domain.Review, error) {
	if !isUUID(orderID) {
		return nil, domain.NewNotFoundError("this order has no review")
	}
	rv, err := scanReview(r.pool.QueryRow(ctx, `SELECT `+reviewCols+` FROM order_reviews WHERE order_id = $1`, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NewNotFoundError("this order has no review")
	}
	return rv, err
}

func (r *ReviewRepository) ListByRestaurant(ctx context.Context, restaurantID string, limit, offset int) ([]domain.Review, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+reviewCols+` FROM order_reviews WHERE restaurant_id = $1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, restaurantID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Review{}
	for rows.Next() {
		rv, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rv)
	}
	return out, rows.Err()
}

func (r *ReviewRepository) Summaries(ctx context.Context, restaurantIDs []string) ([]domain.ReviewSummary, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT restaurant_id, count(*), avg(rating)::float8,
		        count(*) FILTER (WHERE rating = 1), count(*) FILTER (WHERE rating = 2),
		        count(*) FILTER (WHERE rating = 3), count(*) FILTER (WHERE rating = 4),
		        count(*) FILTER (WHERE rating = 5)
		 FROM order_reviews WHERE restaurant_id = ANY($1::uuid[])
		 GROUP BY restaurant_id`, restaurantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReviewSummary{}
	for rows.Next() {
		var s domain.ReviewSummary
		if err := rows.Scan(&s.RestaurantID, &s.Count, &s.Average, &s.Stars[0], &s.Stars[1], &s.Stars[2], &s.Stars[3], &s.Stars[4]); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ReviewRepository) Delete(ctx context.Context, orderID string) error {
	if !isUUID(orderID) {
		return domain.NewNotFoundError("this order has no review")
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM order_reviews WHERE order_id = $1`, orderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewNotFoundError("this order has no review")
	}
	return nil
}
