package application

import (
	"context"

	"goodfood/order-service/internal/domain"
)

type PaymentIntentOutput struct {
	IntentID     string
	ClientSecret string
	AmountCents  int64
	Currency     string
}

// CreatePaymentIntent delegates to payment-service (which owns Stripe and the
// payment record) and moves the order to PAYMENT_PENDING.
func (uc *UseCases) CreatePaymentIntent(ctx context.Context, actor Actor, orderID string) (*PaymentIntentOutput, error) {
	order, err := uc.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !order.IsOwnedBy(actor.UserID) {
		return nil, domain.NewForbiddenError("you can only pay for your own orders")
	}
	if order.Status != domain.StatusPlaced && order.Status != domain.StatusPaymentPending {
		return nil, domain.NewConflictError("order is not awaiting payment")
	}

	const currency = "eur"
	intent, err := uc.payments.CreateIntent(ctx, actor.Token, order.ID, order.TotalAmountCents, currency)
	if err != nil {
		return nil, domain.NewPaymentError(err.Error())
	}

	if order.Status == domain.StatusPlaced {
		if err := order.TransitionTo(domain.StatusPaymentPending); err != nil {
			return nil, err
		}
		if err := uc.orders.UpdateStatus(ctx, order); err != nil {
			return nil, err
		}
	}

	return &PaymentIntentOutput{
		IntentID:     intent.ID,
		ClientSecret: intent.ClientSecret,
		AmountCents:  order.TotalAmountCents,
		Currency:     currency,
	}, nil
}
