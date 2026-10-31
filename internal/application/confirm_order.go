package application

import (
	"context"
	"errors"
	"strings"

	zlog "github.com/rs/zerolog/log"

	"goodfood/order-service/internal/domain"
)

// ConfirmOrder moves a paid order to CONFIRMED. The payment is read from
// payment-service (reconciled with the provider) and must have succeeded for
// exactly this order's amount and customer: nothing the caller says about the
// payment is trusted. Idempotent. In production the payment.succeeded event
// (Stripe webhook) confirms the order even if the customer never comes back.
func (uc *UseCases) ConfirmOrder(ctx context.Context, actor Actor, orderID string) (*domain.Order, error) {
	order, err := uc.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !order.IsOwnedBy(actor.UserID) {
		return nil, domain.NewForbiddenError("you can only confirm your own orders")
	}
	return uc.confirmPaidOrder(ctx, order)
}

// confirmPaidOrder is the confirmation shared by the customer flow and the
// payment.succeeded event.
func (uc *UseCases) confirmPaidOrder(ctx context.Context, order *domain.Order) (*domain.Order, error) {
	if order.Status != domain.StatusPaymentPending {
		if order.ConfirmedAt != nil {
			return order, nil // idempotent: already confirmed (or further along)
		}
		return nil, domain.NewConflictError("order is not awaiting payment (status " + string(order.Status) + ")")
	}

	payment, err := uc.payments.GetPayment(ctx, order.ID)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Code == domain.ErrCodeNotFound {
			return nil, domain.NewPaymentError("no payment found for this order")
		}
		return nil, domain.NewPaymentError(err.Error())
	}
	if err := verifyPayment(order, payment); err != nil {
		return nil, err
	}

	if err := order.TransitionTo(domain.StatusConfirmed); err != nil {
		return nil, err
	}
	// Payment succeeded: persist the confirmation and the pending consumption
	// in one write, detached from the request so a client disconnect cannot
	// leave a paid order unconfirmed or untracked.
	persistCtx, cancel := detached(ctx)
	defer cancel()
	applied, err := uc.orders.ConfirmWithStockSync(persistCtx, order, domain.StockSyncConsumePending, domain.NewOrderEvent(order))
	if err != nil {
		return nil, err
	}
	if !applied {
		return uc.confirmLostRace(persistCtx, order.ID)
	}
	order.StockSync = domain.StockSyncConsumePending
	uc.redeemPromo(persistCtx, order)
	uc.consumeStock(ctx, order)
	return order, nil
}

// verifyPayment refuses a payment that does not settle this very order.
func verifyPayment(order *domain.Order, payment *domain.Payment) error {
	if payment.Status != domain.PaymentStatusSucceeded {
		return domain.NewPaymentError("payment not completed (status: " + payment.Status + ")")
	}
	if payment.AmountCents != order.AmountDueCents() || payment.CustomerID != order.CustomerID ||
		!strings.EqualFold(payment.Currency, orderCurrency) {
		zlog.Error().Str("order_id", order.ID).
			Int64("paid_cents", payment.AmountCents).Int64("due_cents", order.AmountDueCents()).
			Msg("payment does not match the order: confirmation refused")
		return domain.NewPaymentError("the payment does not match this order")
	}
	return nil
}

// redeemPromo consumes the promo code once the order is paid. The customer
// already paid the discounted amount, so a failure (code exhausted or expired
// in between, promo-service down) never blocks the confirmation: it is logged
// for follow-up.
func (uc *UseCases) redeemPromo(ctx context.Context, order *domain.Order) {
	if order.PromoCode == nil {
		return
	}
	if _, err := uc.promos.Redeem(ctx, *order.PromoCode, order.ID, order.CustomerID, order.TotalAmountCents); err != nil {
		zlog.Warn().Err(err).Str("order_id", order.ID).Str("promo_code", *order.PromoCode).
			Msg("promo redemption failed after payment; order confirmed anyway")
	}
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
