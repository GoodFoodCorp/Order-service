// Package stockclient talks to stock-service's internal endpoints. The shared
// service key authenticates this service; it is never logged or included in
// errors.
package stockclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	zlog "github.com/rs/zerolog/log"

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

type consumeResponse struct {
	Status     string `json:"status"`
	Shortfalls []struct {
		StockItemID string  `json:"stockItemId"`
		Shortfall   float64 `json:"shortfall"`
	} `json:"shortfalls"`
}

// maxResponseBytes caps how much of a stock-service response is read.
const maxResponseBytes = 1 << 20

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
// A 2xx means stock-service recorded the consumption; the body is only
// inspected to make drift visible in the logs (a consume skipped because the
// order was already cancelled on stock's side, or ingredients that were short),
// never to fail the call.
func (c *Client) Consume(ctx context.Context, orderID, restaurantID string, lines []domain.StockLine) error {
	body, err := c.do(ctx, "/internal/stocks/consume", consumeRequest{OrderID: orderID, TenantID: restaurantID, Lines: toDTO(lines)})
	if err != nil {
		return err
	}
	var parsed consumeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		zlog.Warn().Err(err).Str("order_id", orderID).Msg("unreadable stock-service consume response (consumption was accepted)")
		return nil
	}
	if parsed.Status == "SKIPPED_CANCELLED" {
		zlog.Warn().Str("order_id", orderID).Str("status", parsed.Status).
			Msg("stock-service skipped the consumption: the order is already cancelled on its side")
	}
	if len(parsed.Shortfalls) > 0 {
		ev := zlog.Warn().Str("order_id", orderID).Str("status", parsed.Status)
		items := make([]string, 0, len(parsed.Shortfalls))
		for _, sf := range parsed.Shortfalls {
			items = append(items, fmt.Sprintf("%s:%g", sf.StockItemID, sf.Shortfall))
		}
		ev.Strs("shortfalls", items).Msg("stock consumed with shortfalls: ingredients were insufficient")
	}
	return nil
}

// Restore puts back what an order consumed (idempotent on the stock side).
func (c *Client) Restore(ctx context.Context, orderID, restaurantID string) error {
	return c.post(ctx, "/internal/stocks/restore", restoreRequest{OrderID: orderID, TenantID: restaurantID}, nil)
}

func (c *Client) post(ctx context.Context, path string, payload, out any) error {
	body, err := c.do(ctx, path, payload)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("invalid stock-service response: %w", err)
	}
	return nil
}

// do sends the request and returns the body of a 2xx response.
func (c *Client) do(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("stock-service request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", c.serviceKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stock-service unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e errorResponse
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&e)
		if e.Error != "" {
			return nil, fmt.Errorf("stock-service returned %d: %s", resp.StatusCode, e.Error)
		}
		return nil, fmt.Errorf("stock-service returned %d", resp.StatusCode)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("invalid stock-service response: %w", err)
	}
	return respBody, nil
}
