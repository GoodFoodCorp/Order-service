// Package promoclient talks to promo-service, which owns promo codes and
// their redemptions. The customer's own JWT is forwarded so a redemption
// stays attributable to them.
package promoclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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

type promoResponse struct {
	Valid         bool   `json:"valid"`
	Code          string `json:"code"`
	DiscountCents int64  `json:"discount_cents"`
	Error         string `json:"error"`
}

func (c *Client) Preview(ctx context.Context, token, code string, orderAmountCents int64) (*domain.PromoPreview, error) {
	q := url.Values{"code": {code}, "amountCents": {strconv.FormatInt(orderAmountCents, 10)}}
	out, err := c.do(ctx, http.MethodGet, "/api/promos/preview?"+q.Encode(), token, nil)
	if err != nil {
		return nil, err
	}
	return &domain.PromoPreview{Code: out.Code, DiscountCents: out.DiscountCents}, nil
}

func (c *Client) Redeem(ctx context.Context, token, code, orderID string, orderAmountCents int64) (int64, error) {
	body, _ := json.Marshal(map[string]any{
		"code":               code,
		"order_id":           orderID,
		"order_amount_cents": orderAmountCents,
	})
	out, err := c.do(ctx, http.MethodPost, "/api/promos/redeem", token, body)
	if err != nil {
		return 0, err
	}
	return out.DiscountCents, nil
}

func (c *Client) do(ctx context.Context, method, path, token string, body []byte) (*promoResponse, error) {
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
		return nil, fmt.Errorf("promo-service unreachable: %w", err)
	}
	defer resp.Body.Close()

	var out promoResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("invalid promo-service response: %w", err)
	}
	if resp.StatusCode >= 300 {
		message := out.Error
		if message == "" {
			message = fmt.Sprintf("promo-service returned %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("%s", message)
	}
	return &out, nil
}
