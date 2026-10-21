package application

import (
	"context"

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

// setStockSync updates the in-memory order and persists the state. A failure
// to persist is only logged: the order status is already committed and the
// error text never reaches a caller.
func (uc *UseCases) setStockSync(ctx context.Context, order *domain.Order, state domain.StockSyncState) {
	order.StockSync = state
	if err := uc.orders.UpdateStockSync(ctx, order.ID, state); err != nil {
		zlog.Error().Err(err).Str("order_id", order.ID).Str("stock_sync", string(state)).Msg("could not persist stock sync state")
	}
}

// consumeStock decrements the stock once the order is paid and CONFIRMED.
// It is best-effort: on failure the order stays CONFIRMED and is flagged
// CONSUME_PENDING so the retry worker can replay it (Consume is idempotent
// per order id on stock-service's side).
func (uc *UseCases) consumeStock(ctx context.Context, order *domain.Order) {
	recipes, err := uc.recipes.Lookup(ctx, order.RestaurantID, distinctMenuItemIDs(order))
	if err != nil {
		zlog.Warn().Err(err).Str("order_id", order.ID).Msg("recipe lookup failed, stock consumption deferred")
		uc.setStockSync(ctx, order, domain.StockSyncConsumePending)
		return
	}
	needs := domain.RequiredIngredients(order.Items, recipes)
	if len(needs) == 0 {
		uc.setStockSync(ctx, order, domain.StockSyncNone)
		return
	}
	if err := uc.stock.Consume(ctx, order.ID, order.RestaurantID, needs); err != nil {
		zlog.Warn().Err(err).Str("order_id", order.ID).Msg("stock consumption failed, deferred")
		uc.setStockSync(ctx, order, domain.StockSyncConsumePending)
		return
	}
	uc.setStockSync(ctx, order, domain.StockSyncConsumed)
}

// restoreStock puts the ingredients back after a cancellation. It only acts
// when stock may have been consumed: CONSUMED, or CONSUME_PENDING (the call
// still goes out so stock-service records a tombstone that makes a delayed
// consume a no-op). Best-effort: failure leaves RESTORE_PENDING for the worker.
func (uc *UseCases) restoreStock(ctx context.Context, order *domain.Order) {
	if order.StockSync != domain.StockSyncConsumed && order.StockSync != domain.StockSyncConsumePending {
		return
	}
	if err := uc.stock.Restore(ctx, order.ID, order.RestaurantID); err != nil {
		zlog.Warn().Err(err).Str("order_id", order.ID).Msg("stock restoration failed, deferred")
		uc.setStockSync(ctx, order, domain.StockSyncRestorePending)
		return
	}
	uc.setStockSync(ctx, order, domain.StockSyncRestored)
}
