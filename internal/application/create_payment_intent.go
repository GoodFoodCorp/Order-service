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

// orderCurrency is the only currency the platform sells in.
const orderCurrency = "eur"

// CreatePaymentIntent delegates to payment-service (which owns Stripe and the
// payment record) and moves the order to PAYMENT_PENDING. The amount sent is
// the order's own amount due: the customer only chooses the order to pay and,
// optionally, one of their saved cards.
func (uc *UseCases) CreatePaymentIntent(ctx context.Context, actor Actor, orderID, paymentMethodID string) (*PaymentIntentOutput, error) {
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

	intent, err := uc.payments.CreateIntent(ctx, domain.PaymentIntentRequest{
		OrderID:         order.ID,
		CustomerID:      order.CustomerID,
		AmountCents:     order.AmountDueCents(),
		Currency:        orderCurrency,
		PaymentMethodID: paymentMethodID,
	})
	if err != nil {
		return nil, domain.NewPaymentError(err.Error())
	}

	if order.Status == domain.StatusPlaced {
		if err := order.TransitionTo(domain.StatusPaymentPending); err != nil {
			return nil, err
		}
		if err := uc.orders.UpdateStatus(ctx, order, domain.StatusPlaced); err != nil {
			return nil, err
		}
	}

	return &PaymentIntentOutput{
		IntentID:     intent.ID,
		ClientSecret: intent.ClientSecret,
		AmountCents:  order.AmountDueCents(),
		Currency:     orderCurrency,
	}, nil
}
