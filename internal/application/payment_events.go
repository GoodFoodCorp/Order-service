package application

import (
	"context"
	"errors"

	zlog "github.com/rs/zerolog/log"

	"goodfood/order-service/internal/domain"
)

// HandlePaymentSucceeded confirms the order of a payment.succeeded event
// (Stripe webhook relayed by payment-service): the customer who closed the
// tab right after paying still gets their order. The payment is re-checked
// against payment-service like any confirmation. Business refusals (order
// cancelled meanwhile, amount mismatch) are final and only logged; anything
// else is returned so the message is retried.
func (uc *UseCases) HandlePaymentSucceeded(ctx context.Context, orderID string) error {
	order, err := uc.orders.GetByID(ctx, orderID)
	if err != nil {
		if isDomainError(err, domain.ErrCodeNotFound) {
			zlog.Warn().Str("order_id", orderID).Msg("payment.succeeded for an unknown order, ignored")
			return nil
		}
		return err
	}
	if _, err := uc.confirmPaidOrder(ctx, order); err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Code != domain.ErrCodePayment && de.Code != domain.ErrCodeUnavailable {
			zlog.Warn().Err(err).Str("order_id", orderID).Msg("payment.succeeded not applied")
			return nil
		}
		return err
	}
	return nil
}

func isDomainError(err error, code domain.ErrorCode) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Code == code
}
