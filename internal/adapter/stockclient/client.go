// Package stockclient talks to stock-service's internal endpoints. The shared
// service key authenticates this service; it is never logged or included in
// errors.
package stockclient

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
	baseURL    string
	serviceKey string
	http       *http.Client
}

var _ domain.StockService = (*Client)(nil)

func New(baseURL, serviceKey string) *Client {
	return &Client{baseURL: baseURL, serviceKey: serviceKey, http: &http.Client{Timeout: 10 * time.Second}}
}

type lineDTO struct {
	StockItemID string  `json:"stockItemId"`
	Quantity    float64 `json:"quantity"`
}

type checkRequest struct {
	TenantID string    `json:"tenantId"`
	Lines    []lineDTO `json:"lines"`
}

type consumeRequest struct {
	OrderID  string    `json:"orderId"`
	TenantID string    `json:"tenantId"`
	Lines    []lineDTO `json:"lines"`
}

type restoreRequest struct {
	OrderID  string `json:"orderId"`
	TenantID string `json:"tenantId"`
}

type checkResponse struct {
	Available bool `json:"available"`
	Missing   []struct {
		StockItemID string  `json:"stockItemId"`
		Name        string  `json:"name"`
		Unit        string  `json:"unit"`
		Required    float64 `json:"required"`
		OnHand      float64 `json:"onHand"`
	} `json:"missing"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func toDTO(lines []domain.StockLine) []lineDTO {
	out := make([]lineDTO, 0, len(lines))
	for _, l := range lines {
		out = append(out, lineDTO{StockItemID: l.StockItemID, Quantity: l.Quantity})
	}
	return out
}

// Check asks whether the stock of the restaurant covers the given lines.
func (c *Client) Check(ctx context.Context, restaurantID string, lines []domain.StockLine) (*domain.StockAvailability, error) {
	var parsed checkResponse
	if err := c.post(ctx, "/internal/stocks/check", checkRequest{TenantID: restaurantID, Lines: toDTO(lines)}, &parsed); err != nil {
		return nil, err
	}
	av := &domain.StockAvailability{Available: parsed.Available}
	for _, m := range parsed.Missing {
		av.Missing = append(av.Missing, domain.MissingStock{
			StockItemID: m.StockItemID, Name: m.Name, Unit: m.Unit, Required: m.Required, OnHand: m.OnHand,
		})
	}
	return av, nil
}

// Consume decrements the stock for an order (idempotent on the stock side).
func (c *Client) Consume(ctx context.Context, orderID, restaurantID string, lines []domain.StockLine) error {
	return c.post(ctx, "/internal/stocks/consume", consumeRequest{OrderID: orderID, TenantID: restaurantID, Lines: toDTO(lines)}, nil)
}

// Restore puts back what an order consumed (idempotent on the stock side).
func (c *Client) Restore(ctx context.Context, orderID, restaurantID string) error {
	return c.post(ctx, "/internal/stocks/restore", restoreRequest{OrderID: orderID, TenantID: restaurantID}, nil)
}

func (c *Client) post(ctx context.Context, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("stock-service request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", c.serviceKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("stock-service unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e errorResponse
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error != "" {
			return fmt.Errorf("stock-service returned %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("stock-service returned %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("invalid stock-service response: %w", err)
	}
	return nil
}
