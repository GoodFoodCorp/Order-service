package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"goodfood/order-service/internal/domain"
)

// orderCols lists the order columns every read selects, in orderDest order.
const orderCols = `id, customer_id, restaurant_id, status, total_amount_cents, delivery_address, placed_at, confirmed_at, promo_code, discount_cents, stock_sync, courier_id, refund_sync`

// orderDest returns the scan destinations matching orderCols.
func orderDest(o *domain.Order) []any {
	return []any{&o.ID, &o.CustomerID, &o.RestaurantID, &o.Status, &o.TotalAmountCents,
		&o.DeliveryAddress, &o.PlacedAt, &o.ConfirmedAt, &o.PromoCode, &o.DiscountCents, &o.StockSync, &o.CourierID, &o.RefundSync}
}

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
		`INSERT INTO orders (id, customer_id, restaurant_id, status, total_amount_cents, delivery_address, placed_at, confirmed_at, promo_code, discount_cents, stock_sync)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		order.ID, order.CustomerID, order.RestaurantID, order.Status,
		order.TotalAmountCents, order.DeliveryAddress, order.PlacedAt, order.ConfirmedAt,
		order.PromoCode, order.DiscountCents, stockSyncOrNone(order.StockSync))
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
		`SELECT ` + orderCols + `
		 FROM orders WHERE id = $1`, id).
		Scan(orderDest(&o)...)
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
		`SELECT ` + orderCols + `
		 FROM orders WHERE customer_id = $1 ORDER BY placed_at DESC`, customerID)
}

func (r *OrderRepository) ListByRestaurant(ctx context.Context, restaurantID string) ([]domain.Order, error) {
	return r.list(ctx,
		`SELECT ` + orderCols + `
		 FROM orders WHERE restaurant_id = $1 ORDER BY placed_at DESC`, restaurantID)
}

func (r *OrderRepository) ListByStatuses(ctx context.Context, statuses ...domain.OrderStatus) ([]domain.Order, error) {
	raw := make([]string, len(statuses))
	for i, s := range statuses {
		raw[i] = string(s)
	}
	return r.list(ctx,
		`SELECT ` + orderCols + `
		 FROM orders WHERE status = ANY($1) ORDER BY placed_at ASC`, raw)
}

// errConcurrentChange is returned when a guarded status write found the order
// in another status than the one the use case read.
func errConcurrentChange() error {
	return domain.NewConflictError("order changed concurrently, retry")
}

// withEvent runs write in a transaction and, if it applied, records event
// (when not nil) in the outbox before committing.
func (r *OrderRepository) withEvent(ctx context.Context, event *domain.Event, write func(tx pgx.Tx) (bool, error)) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	applied, err := write(tx)
	if err != nil || !applied {
		return false, err
	}
	if event != nil {
		if err := insertEvent(ctx, tx, event); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

func (r *OrderRepository) UpdateStatus(ctx context.Context, order *domain.Order, from domain.OrderStatus, event *domain.Event) error {
	applied, err := r.withEvent(ctx, event, func(tx pgx.Tx) (bool, error) {
		tag, err := tx.Exec(ctx,
			`UPDATE orders SET status = $1, confirmed_at = $2 WHERE id = $3 AND status = $4`,
			order.Status, order.ConfirmedAt, order.ID, from)
		return tag.RowsAffected() > 0, err
	})
	if err != nil {
		return err
	}
	if applied {
		return nil
	}
	current, err := r.currentStatus(ctx, order.ID)
	if err != nil {
		return err
	}
	if current == order.Status {
		return nil // a concurrent identical update already applied it
	}
	return errConcurrentChange()
}

func (r *OrderRepository) ConfirmWithStockSync(ctx context.Context, order *domain.Order, stockSync domain.StockSyncState, event *domain.Event) (bool, error) {
	// Guarded on PAYMENT_PENDING, the only legal predecessor of CONFIRMED: a
	// late duplicate confirmation can never reset a cancelled (and restored)
	// order to CONFIRMED|CONSUME_PENDING.
	return r.withEvent(ctx, event, func(tx pgx.Tx) (bool, error) {
		tag, err := tx.Exec(ctx,
			`UPDATE orders SET status = $1, confirmed_at = $2, stock_sync = $3
			 WHERE id = $4 AND status = 'PAYMENT_PENDING'`,
			order.Status, order.ConfirmedAt, string(stockSync), order.ID)
		return tag.RowsAffected() > 0, err
	})
}

func (r *OrderRepository) CancelWithStockSync(ctx context.Context, order *domain.Order, event *domain.Event) error {
	// Restorability is computed from the row itself, not from the status the
	// use case read: in an UPDATE, the SET expressions see the pre-update row
	// (re-evaluated on the latest version under concurrent writes), so a
	// confirmation that committed after the read is still restored. The WHERE
	// keeps the lifecycle rule (only these statuses may be cancelled).
	var state domain.StockSyncState
	applied, err := r.withEvent(ctx, event, func(tx pgx.Tx) (bool, error) {
		err := tx.QueryRow(ctx,
			`UPDATE orders SET status = 'CANCELLED',
			   stock_sync = CASE
			     WHEN status IN ('CONFIRMED', 'IN_PREPARATION', 'READY_FOR_PICKUP')
			      AND stock_sync IN ('CONSUMED', 'CONSUME_PENDING') THEN 'RESTORE_PENDING'
			     ELSE stock_sync END,
			   refund_sync = CASE
			     WHEN status IN ('PAYMENT_PENDING', 'CONFIRMED', 'IN_PREPARATION', 'READY_FOR_PICKUP') THEN 'PENDING'
			     ELSE refund_sync END
			 WHERE id = $1 AND status IN ('PLACED', 'PAYMENT_PENDING', 'CONFIRMED', 'IN_PREPARATION', 'READY_FOR_PICKUP')
			 RETURNING stock_sync, refund_sync, confirmed_at`,
			order.ID).Scan(&state, &order.RefundSync, &order.ConfirmedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		return err
	}
	if !applied {
		if _, err := r.currentStatus(ctx, order.ID); err != nil {
			return err
		}
		return errConcurrentChange()
	}
	order.Status = domain.StatusCancelled
	order.StockSync = state
	return nil
}

// currentStatus re-reads the persisted status after a guarded write matched no
// row, to tell a missing order from a concurrent change.
func (r *OrderRepository) currentStatus(ctx context.Context, id string) (domain.OrderStatus, error) {
	var status domain.OrderStatus
	err := r.pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.NewNotFoundError("order not found")
	}
	return status, err
}

func (r *OrderRepository) UpdateStockSync(ctx context.Context, orderID string, from, to domain.StockSyncState) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE orders SET stock_sync = $3 WHERE id = $1 AND stock_sync = $2`,
		orderID, string(from), string(to))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *OrderRepository) ListByStockSync(ctx context.Context, states []domain.StockSyncState, limit int) ([]domain.Order, error) {
	values := make([]string, len(states))
	for i, s := range states {
		values[i] = string(s)
	}
	return r.list(ctx,
		`SELECT ` + orderCols + `
		 FROM orders WHERE stock_sync = ANY($1) ORDER BY placed_at ASC LIMIT $2`, values, limit)
}

// stockSyncOrNone defaults an unset state to NONE (the column is NOT NULL).
func stockSyncOrNone(s domain.StockSyncState) domain.StockSyncState {
	if s == "" {
		return domain.StockSyncNone
	}
	return s
}

func (r *OrderRepository) list(ctx context.Context, query string, args ...any) ([]domain.Order, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := []domain.Order{}
	for rows.Next() {
		var o domain.Order
		if err := rows.Scan(orderDest(&o)...); err != nil {
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

func (r *OrderRepository) AssignCourier(ctx context.Context, orderID, courierID string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE orders SET courier_id = $2
		 WHERE id = $1 AND status IN ('IN_PREPARATION', 'READY_FOR_PICKUP')
		   AND (courier_id IS NULL OR courier_id = $2)`, orderID, courierID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *OrderRepository) UnassignCourier(ctx context.Context, orderID, courierID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE orders SET courier_id = NULL WHERE id = $1 AND courier_id = $2 AND status IN ('IN_PREPARATION', 'READY_FOR_PICKUP')`,
		orderID, courierID)
	return err
}

func (r *OrderRepository) UpdateRefundSync(ctx context.Context, orderID string, from, to domain.RefundSyncState) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE orders SET refund_sync = $3 WHERE id = $1 AND refund_sync = $2`, orderID, string(from), string(to))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *OrderRepository) ListByRefundSync(ctx context.Context, state domain.RefundSyncState, limit int) ([]domain.Order, error) {
	return r.list(ctx,
		`SELECT `+orderCols+` FROM orders WHERE refund_sync = $1 ORDER BY placed_at ASC LIMIT $2`, string(state), limit)
}
