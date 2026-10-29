// Package paymentclient talks to payment-service's internal API (service key),
// which owns Stripe and every payment record. This service is the only one
// allowed to create, reconcile or refund a payment, always for the amount it
// priced itself.
package paymentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"goodfood/order-service/internal/domain"
)

const serviceKeyHeader = "X-Service-Key"

type Client struct {
	baseURL    string
	serviceKey string
	http       *http.Client
}

func New(baseURL, serviceKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: baseURL, serviceKey: serviceKey, http: httpClient}
}

type paymentResponse struct {
	OrderID      string `json:"order_id"`
	CustomerID   string `json:"customer_id"`
	IntentID     string `json:"intent_id"`
	ClientSecret string `json:"client_secret"`
	Status       string `json:"status"`
	AmountCents  int64  `json:"amount_cents"`
	Currency     string `json:"currency"`
}

func (c *Client) CreateIntent(ctx context.Context, req domain.PaymentIntentRequest) (*domain.PaymentIntent, error) {
	body, err := json.Marshal(map[string]any{
		"order_id":          req.OrderID,
		"customer_id":       req.CustomerID,
		"amount_cents":      req.AmountCents,
		"currency":          req.Currency,
		"payment_method_id": req.PaymentMethodID,
	})
	if err != nil {
		return nil, err
	}
	var out paymentResponse
	if err := c.do(ctx, http.MethodPost, "/internal/payments/intents", body, &out); err != nil {
		return nil, err
	}
	return &domain.PaymentIntent{
		ID:           out.IntentID,
		ClientSecret: out.ClientSecret,
		Status:       out.Status,
		AmountCents:  out.AmountCents,
		Currency:     out.Currency,
	}, nil
}

func (c *Client) GetPayment(ctx context.Context, orderID string) (*domain.Payment, error) {
	var out paymentResponse
	if err := c.do(ctx, http.MethodGet, "/internal/payments/"+url.PathEscape(orderID), nil, &out); err != nil {
		return nil, err
	}
	return &domain.Payment{
		OrderID:     out.OrderID,
		CustomerID:  out.CustomerID,
		Status:      out.Status,
		AmountCents: out.AmountCents,
		Currency:    out.Currency,
	}, nil
}

func (c *Client) Refund(ctx context.Context, orderID string) error {
	return c.do(ctx, http.MethodPost, "/internal/payments/"+url.PathEscape(orderID)+"/refund", nil, nil)
}

// do sends one call. A 404 becomes a domain NOT_FOUND error (no payment for
// this order); any other non-2xx carries payment-service's message.
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(serviceKeyHeader, c.serviceKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("payment-service unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return domain.NewNotFoundError("no payment for this order")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("payment-service returned %d", resp.StatusCode)
		}
		return fmt.Errorf("%s", e.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("invalid payment-service response: %w", err)
	}
	return nil
}
