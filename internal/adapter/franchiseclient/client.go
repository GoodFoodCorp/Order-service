// Package franchiseclient asks franchise-service (internal API, service key)
// whether a restaurant can take an order now, and at what delivery fee.
package franchiseclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"goodfood/order-service/internal/domain"
)

type Client struct {
	baseURL    string
	serviceKey string
	http       *http.Client
}

func New(baseURL, serviceKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{baseURL: baseURL, serviceKey: serviceKey, http: httpClient}
}

type quoteResponse struct {
	CanOrder                 bool     `json:"canOrder"`
	Refusal                  *string  `json:"refusal"`
	DeliveryFeeCents         int64    `json:"deliveryFeeCents"`
	MinimumOrderCents        int64    `json:"minimumOrderCents"`
	PrepTimeMinutes          int      `json:"prepTimeMinutes"`
	EstimatedDeliveryMinutes int      `json:"estimatedDeliveryMinutes"`
	DropoffLatitude          *float64 `json:"dropoffLatitude"`
	DropoffLongitude         *float64 `json:"dropoffLongitude"`
}

func (c *Client) Quote(ctx context.Context, restaurantID, deliveryAddress string) (*domain.RestaurantQuote, error) {
	body, err := json.Marshal(map[string]any{"address": deliveryAddress})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/restaurants/"+url.PathEscape(restaurantID)+"/quote", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", c.serviceKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("franchise-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, domain.NewNotFoundError("restaurant not found")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("franchise-service returned %d", resp.StatusCode)
	}

	var out quoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("invalid franchise-service response: %w", err)
	}
	refusal := ""
	if out.Refusal != nil {
		refusal = *out.Refusal
	}
	return &domain.RestaurantQuote{
		CanOrder:          out.CanOrder,
		Refusal:           refusal,
		DeliveryFeeCents:  out.DeliveryFeeCents,
		MinimumOrderCents: out.MinimumOrderCents,
		PrepTimeMinutes:   out.PrepTimeMinutes,
		EstimatedMinutes:  out.EstimatedDeliveryMinutes,
		DropoffLat:        out.DropoffLatitude,
		DropoffLng:        out.DropoffLongitude,
	}, nil
}
