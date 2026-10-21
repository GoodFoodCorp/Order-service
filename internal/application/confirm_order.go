package application

import (
	"context"

	zlog "github.com/rs/zerolog/log"

	"goodfood/order-service/internal/domain"
)

// ConfirmOrder asks payment-service to verify the payment, then moves the order
// to CONFIRMED. In production the confirmation would be driven by a Stripe
// webhook received by payment-service; for the POC the client calls it after
// checkout (test mode).
func (uc *UseCases) ConfirmOrder(ctx context.Context, actor Actor, orderID string) (*domain.Order, error) {
	order, err := uc.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !order.IsOwnedBy(actor.UserID) && !actor.HasRole(RoleAdmin) {
		return nil, domain.NewForbiddenError("you can only confirm your own orders")
	}
	if order.Status == domain.StatusConfirmed {
		return order, nil // idempotent
	}

	status, err := uc.payments.Confirm(ctx, actor.Token, order.ID)
	if err != nil {
		return nil, domain.NewPaymentError(err.Error())
	}
	if status != "SUCCEEDED" {
		return nil, domain.NewPaymentError("payment not completed (status: " + status + ")")
	}

	// The promo code is only consumed for real once the payment actually
	// succeeded — a customer who abandons checkout never burns a redemption.
	if order.PromoCode != nil {
		if _, err := uc.promos.Redeem(ctx, actor.Token, *order.PromoCode, order.ID, order.TotalAmountCents); err != nil {
			return nil, domain.NewPaymentError(err.Error())
		}
	}

	if err := order.TransitionTo(domain.StatusConfirmed); err != nil {
		return nil, err
	}
	// Payment succeeded: persist the confirmation and the pending consumption
	// in one write, detached from the request so a client disconnect cannot
	// leave a paid order unconfirmed or untracked.
	persistCtx, cancel := detached(ctx)
	defer cancel()
	applied, err := uc.orders.ConfirmWithStockSync(persistCtx, order, domain.StockSyncConsumePending)
	if err != nil {
		return nil, err
	}
	if !applied {
		return uc.confirmLostRace(persistCtx, order.ID)
	}
	order.StockSync = domain.StockSyncConsumePending
	uc.consumeStock(ctx, order)
	return order, nil
}

// confirmLostRace handles a confirmation whose guarded write found the order
// no longer PAYMENT_PENDING. A concurrent confirmation already owns the stock
// consumption (the retry worker covers its failures), so the current order is
// returned as is. A cancellation that won is reported as a conflict: the
// payment succeeded on a cancelled order, a refund matter outside this flow.
func (uc *UseCases) confirmLostRace(ctx context.Context, orderID string) (*domain.Order, error) {
	current, err := uc.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	switch current.Status {
	case domain.StatusCancelled:
		zlog.Error().Str("order_id", orderID).Msg("payment confirmed for an order cancelled concurrently: needs a manual refund")
		return nil, domain.NewConflictError("order was cancelled while its payment was being confirmed")
	case domain.StatusPlaced, domain.StatusPaymentPending:
		return nil, domain.NewConflictError("order changed concurrently, retry")
	default:
		return current, nil // already confirmed (or further along) by another request
	}
}
