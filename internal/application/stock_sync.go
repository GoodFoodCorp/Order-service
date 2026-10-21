package application

import (
	"context"
	"time"

	zlog "github.com/rs/zerolog/log"

	"goodfood/order-service/internal/domain"
)

// stockRestorableStatuses are the statuses from which a cancellation puts the
// ingredients back (the order was paid, so stock may have been consumed).
var stockRestorableStatuses = map[domain.OrderStatus]bool{
	domain.StatusConfirmed:      true,
	domain.StatusInPreparation:  true,
	domain.StatusReadyForPickup: true,
}

// stockSyncTimeout bounds the detached stock work (lookup, call, state write).
const stockSyncTimeout = 30 * time.Second

// detached returns a context that survives the request being cancelled (a
// client disconnect after payment must not abort stock work), bounded by a
// timeout.
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), stockSyncTimeout)
}

// moveStockSync is a compare-and-set of the persisted state. The in-memory
// order only follows when the write was applied; a lost race or a failed
// write is logged and leaves the persisted state for the retry worker.
func (uc *UseCases) moveStockSync(ctx context.Context, order *domain.Order, from, to domain.StockSyncState) {
	applied, err := uc.orders.UpdateStockSync(ctx, order.ID, from, to)
	switch {
	case err != nil:
		zlog.Error().Err(err).Str("order_id", order.ID).Str("from", string(from)).Str("to", string(to)).Msg("could not persist stock sync state")
	case !applied:
		zlog.Info().Str("order_id", order.ID).Str("from", string(from)).Str("to", string(to)).Msg("stock sync state changed concurrently, write skipped")
	default:
		order.StockSync = to
	}
}

// consumeStock decrements the stock once the order is paid and CONFIRMED
// (stock_sync is CONSUME_PENDING, written with the confirmation). It moves
// CONSUME_PENDING to CONSUMED, or to NONE when no dish is tracked. Any failure
// leaves CONSUME_PENDING for the retry worker (Consume is idempotent per order
// id on stock-service's side). Best-effort and detached from request
// cancellation.
func (uc *UseCases) consumeStock(ctx context.Context, order *domain.Order) {
	ctx, cancel := detached(ctx)
	defer cancel()

	recipes, err := uc.recipes.Lookup(ctx, order.RestaurantID, distinctMenuItemIDs(order))
	if err != nil {
		zlog.Warn().Err(err).Str("order_id", order.ID).Msg("recipe lookup failed, stock consumption deferred")
		return
	}
	needs := domain.RequiredIngredients(order.Items, recipes)
	if len(needs) == 0 {
		uc.moveStockSync(ctx, order, domain.StockSyncConsumePending, domain.StockSyncNone)
		return
	}
	if err := uc.stock.Consume(ctx, order.ID, order.RestaurantID, needs); err != nil {
		zlog.Warn().Err(err).Str("order_id", order.ID).Msg("stock consumption failed, deferred")
		return
	}
	uc.moveStockSync(ctx, order, domain.StockSyncConsumePending, domain.StockSyncConsumed)
}

// restoreStock puts the ingredients back after a cancellation that left the
// order in RESTORE_PENDING (set atomically by CancelWithStockSync, including
// when a consume was still pending: stock-service then records a tombstone
// that makes the delayed consume a no-op). On success RESTORE_PENDING moves to
// RESTORED; on failure it stays for the worker.
func (uc *UseCases) restoreStock(ctx context.Context, order *domain.Order) {
	if order.StockSync != domain.StockSyncRestorePending {
		return
	}
	ctx, cancel := detached(ctx)
	defer cancel()

	if err := uc.stock.Restore(ctx, order.ID, order.RestaurantID); err != nil {
		zlog.Warn().Err(err).Str("order_id", order.ID).Msg("stock restoration failed, deferred")
		return
	}
	uc.moveStockSync(ctx, order, domain.StockSyncRestorePending, domain.StockSyncRestored)
}

// RetryStockSync replays the stock work left pending (stock-service or
// menu-service was down, or the process crashed). Oldest orders first, at most
// limit per pass; each order is handled independently and failures stay
// pending for the next pass. It returns the number of orders handled.
func (uc *UseCases) RetryStockSync(ctx context.Context, limit int) (processed int) {
	pending, err := uc.orders.ListByStockSync(ctx, []domain.StockSyncState{
		domain.StockSyncConsumePending,
		domain.StockSyncRestorePending,
	}, limit)
	if err != nil {
		zlog.Error().Err(err).Msg("could not list orders pending stock synchronisation")
		return 0
	}
	for i := range pending {
		order := &pending[i]
		switch {
		case order.StockSync == domain.StockSyncConsumePending && order.Status == domain.StatusCancelled:
			// A cancel landed while the consume was pending: never replay the
			// consume, restore instead (stock-service's tombstone covers a
			// consume that already applied or is still in flight).
			uc.moveStockSync(ctx, order, domain.StockSyncConsumePending, domain.StockSyncRestorePending)
			uc.restoreStock(ctx, order)
		case order.StockSync == domain.StockSyncConsumePending:
			uc.consumeStock(ctx, order)
		default:
			uc.restoreStock(ctx, order)
		}
		processed++
	}
	return processed
}
