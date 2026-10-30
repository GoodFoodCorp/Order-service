package application

import (
	"context"
	"time"

	zlog "github.com/rs/zerolog/log"

	"goodfood/order-service/internal/domain"
)

// CancelRequest is the reason given for a cancellation (and, for OTHER,
// what happened).
type CancelRequest struct {
	Reason  string
	Comment string
}

// CancelOrder cancels an order with a reason, like on delivery apps: the
// customer until the kitchen starts preparing it, the restaurant (its own
// orders) and head office until the courier has it. A paid order is
// refunded and its stock put back.
func (uc *UseCases) CancelOrder(ctx context.Context, actor Actor, orderID string, req CancelRequest) (*domain.Order, error) {
	order, err := uc.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	var by domain.CancelledBy
	switch {
	case actor.HasRole(RoleAdmin):
		by = domain.CancelledByHeadOffice
	case actor.HasRole(RoleManager):
		if actor.TenantID != order.RestaurantID {
			return nil, domain.NewForbiddenError("managers can only cancel orders of their own restaurant")
		}
		by = domain.CancelledByRestaurant
	case order.IsOwnedBy(actor.UserID):
		if !order.IsCancellableByCustomer() {
			return nil, domain.NewConflictError("the restaurant has already started this order: contact it to cancel")
		}
		by = domain.CancelledByCustomer
	default:
		return nil, domain.NewForbiddenError("you can only cancel your own orders")
	}
	cancellation, err := domain.NewCancellation(by, req.Reason, req.Comment, time.Now())
	if err != nil {
		return nil, err
	}
	if err := order.Cancel(cancellation); err != nil {
		return nil, err
	}
	return uc.cancel(ctx, order)
}

// cancel persists a cancellation (already applied in memory) with its event,
// then puts the ingredients back and refunds the customer. Both follow-ups
// are tracked in the row, so the retry worker finishes them if they fail now.
func (uc *UseCases) cancel(ctx context.Context, order *domain.Order) (*domain.Order, error) {
	// The repository decides restorability and refundability from the
	// persisted row, so a confirmation committed after our read is still
	// restored and refunded.
	if err := uc.orders.CancelWithStockSync(ctx, order, domain.NewOrderEvent(order)); err != nil {
		return nil, err
	}
	uc.restoreStock(ctx, order)
	uc.refundPayment(ctx, order)
	return order, nil
}

// refundPayment pays a cancelled order back (or closes its unpaid intent) and
// frees its promo usage. Detached from the request: a refund must not be
// abandoned because the caller disconnected.
func (uc *UseCases) refundPayment(ctx context.Context, order *domain.Order) {
	if order.RefundSync != domain.RefundSyncPending {
		return
	}
	ctx, cancel := detached(ctx)
	defer cancel()

	if err := uc.payments.Refund(ctx, order.ID); err != nil && !isDomainError(err, domain.ErrCodeNotFound) {
		zlog.Warn().Err(err).Str("order_id", order.ID).Msg("refund failed, will be retried")
		return
	}
	if order.PromoCode != nil {
		if err := uc.promos.Release(ctx, order.ID); err != nil {
			zlog.Warn().Err(err).Str("order_id", order.ID).Msg("promo usage not released, will be retried")
			return
		}
	}
	applied, err := uc.orders.UpdateRefundSync(ctx, order.ID, domain.RefundSyncPending, domain.RefundSyncDone)
	if err != nil {
		zlog.Error().Err(err).Str("order_id", order.ID).Msg("could not record the refund")
		return
	}
	if applied {
		order.RefundSync = domain.RefundSyncDone
	}
}

// RetryRefunds replays the refunds left pending (payment-service or the
// provider was down). Payment-service refunds idempotently per order.
func (uc *UseCases) RetryRefunds(ctx context.Context, limit int) (processed int) {
	pending, err := uc.orders.ListByRefundSync(ctx, domain.RefundSyncPending, limit)
	if err != nil {
		if ctx.Err() == nil {
			zlog.Error().Err(err).Msg("could not list orders pending refund")
		}
		return 0
	}
	for i := range pending {
		if ctx.Err() != nil {
			break
		}
		uc.refundPayment(ctx, &pending[i])
		processed++
	}
	return processed
}
