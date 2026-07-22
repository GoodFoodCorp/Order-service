package application

import (
	"context"

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

	if err := order.TransitionTo(domain.StatusConfirmed); err != nil {
		return nil, err
	}
	if err := uc.orders.UpdateStatus(ctx, order); err != nil {
		return nil, err
	}
	return order, nil
}
