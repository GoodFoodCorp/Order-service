package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"goodfood/order-service/internal/domain"
)

type OrderRepository struct {
	pool *pgxpool.Pool
}

func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

func (r *OrderRepository) Create(ctx context.Context, order *domain.Order) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	_, err = tx.Exec(ctx,
		`INSERT INTO orders (id, customer_id, restaurant_id, status, total_amount_cents, delivery_address, placed_at, confirmed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		order.ID, order.CustomerID, order.RestaurantID, order.Status,
		order.TotalAmountCents, order.DeliveryAddress, order.PlacedAt, order.ConfirmedAt)
	if err != nil {
		return err
	}

	for _, it := range order.Items {
		_, err = tx.Exec(ctx,
			`INSERT INTO order_items (id, order_id, menu_item_id, menu_item_name, quantity, unit_price_cents, special_instructions)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			it.ID, it.OrderID, it.MenuItemID, it.MenuItemName, it.Quantity, it.UnitPriceCents, it.SpecialInstructions)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *OrderRepository) GetByID(ctx context.Context, id string) (*domain.Order, error) {
	var o domain.Order
	err := r.pool.QueryRow(ctx,
		`SELECT id, customer_id, restaurant_id, status, total_amount_cents, delivery_address, placed_at, confirmed_at
		 FROM orders WHERE id = $1`, id).
		Scan(&o.ID, &o.CustomerID, &o.RestaurantID, &o.Status, &o.TotalAmountCents,
			&o.DeliveryAddress, &o.PlacedAt, &o.ConfirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NewNotFoundError("order not found")
	}
	if err != nil {
		return nil, err
	}

	items, err := r.loadItems(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	o.Items = items
	return &o, nil
}

func (r *OrderRepository) ListByCustomer(ctx context.Context, customerID string) ([]domain.Order, error) {
	return r.list(ctx,
		`SELECT id, customer_id, restaurant_id, status, total_amount_cents, delivery_address, placed_at, confirmed_at
		 FROM orders WHERE customer_id = $1 ORDER BY placed_at DESC`, customerID)
}

func (r *OrderRepository) ListByRestaurant(ctx context.Context, restaurantID string) ([]domain.Order, error) {
	return r.list(ctx,
		`SELECT id, customer_id, restaurant_id, status, total_amount_cents, delivery_address, placed_at, confirmed_at
		 FROM orders WHERE restaurant_id = $1 ORDER BY placed_at DESC`, restaurantID)
}

func (r *OrderRepository) ListByStatus(ctx context.Context, status domain.OrderStatus) ([]domain.Order, error) {
	return r.list(ctx,
		`SELECT id, customer_id, restaurant_id, status, total_amount_cents, delivery_address, placed_at, confirmed_at
		 FROM orders WHERE status = $1 ORDER BY placed_at ASC`, string(status))
}

func (r *OrderRepository) UpdateStatus(ctx context.Context, order *domain.Order) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE orders SET status = $1, confirmed_at = $2 WHERE id = $3`,
		order.Status, order.ConfirmedAt, order.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewNotFoundError("order not found")
	}
	return nil
}

func (r *OrderRepository) list(ctx context.Context, query string, arg any) ([]domain.Order, error) {
	rows, err := r.pool.Query(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := []domain.Order{}
	for rows.Next() {
		var o domain.Order
		if err := rows.Scan(&o.ID, &o.CustomerID, &o.RestaurantID, &o.Status,
			&o.TotalAmountCents, &o.DeliveryAddress, &o.PlacedAt, &o.ConfirmedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Load items for each order (lists stay small in the POC).
	for i := range orders {
		items, err := r.loadItems(ctx, orders[i].ID)
		if err != nil {
			return nil, err
		}
		orders[i].Items = items
	}
	return orders, nil
}

func (r *OrderRepository) loadItems(ctx context.Context, orderID string) ([]domain.OrderItem, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, order_id, menu_item_id, menu_item_name, quantity, unit_price_cents, special_instructions
		 FROM order_items WHERE order_id = $1`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.OrderItem{}
	for rows.Next() {
		var it domain.OrderItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.MenuItemID, &it.MenuItemName,
			&it.Quantity, &it.UnitPriceCents, &it.SpecialInstructions); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}
