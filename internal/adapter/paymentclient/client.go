// Package paymentclient talks to payment-service, which owns Stripe and all
// payment records (extracted from this service). The customer's own JWT is
// forwarded so payments stay attributable to them.
package paymentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"goodfood/order-service/internal/domain"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 10 * time.Second}}
}

type paymentResponse struct {
	OrderID      string `json:"order_id"`
	IntentID     string `json:"intent_id"`
	ClientSecret string `json:"client_secret"`
	Status       string `json:"status"`
	AmountCents  int64  `json:"amount_cents"`
	Currency     string `json:"currency"`
	Error        string `json:"error"`
}

func (c *Client) CreateIntent(ctx context.Context, token, orderID string, amountCents int64, currency string) (*domain.PaymentIntent, error) {
	body, _ := json.Marshal(map[string]any{
		"order_id":     orderID,
		"amount_cents": amountCents,
		"currency":     currency,
	})
	out, err := c.do(ctx, http.MethodPost, "/api/payments/intents", token, body)
	if err != nil {
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

// Confirm asks payment-service to verify the payment with the provider.
// Returns the resulting payment status (e.g. "SUCCEEDED").
func (c *Client) Confirm(ctx context.Context, token, orderID string) (string, error) {
	out, err := c.do(ctx, http.MethodPost, "/api/payments/"+orderID+"/confirm", token, nil)
	if err != nil {
		return "", err
	}
	return out.Status, nil
}

func (c *Client) do(ctx context.Context, method, path, token string, body []byte) (*paymentResponse, error) {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader([]byte{})
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("payment-service unreachable: %w", err)
	}
	defer resp.Body.Close()

	var out paymentResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("invalid payment-service response: %w", err)
	}
	if resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = fmt.Sprintf("payment-service returned %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("%s", message)
	}
	return &out, nil
}
