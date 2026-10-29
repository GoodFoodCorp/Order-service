// Package promoclient talks to promo-service, which owns promo codes and
// their redemptions. Previews forward the customer's JWT (public route);
// redemptions and releases use the internal API (service key), since they
// also happen without any customer request (payment webhook, cancellation).
package promoclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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

type promoResponse struct {
	Code          string `json:"code"`
	DiscountCents int64  `json:"discount_cents"`
}

func (c *Client) Preview(ctx context.Context, token, code string, orderAmountCents int64) (*domain.PromoPreview, error) {
	q := url.Values{"code": {code}, "amountCents": {strconv.FormatInt(orderAmountCents, 10)}}
	var out promoResponse
	if err := c.do(ctx, http.MethodGet, "/api/promos/preview?"+q.Encode(), "Authorization", "Bearer "+token, nil, &out); err != nil {
		return nil, err
	}
	return &domain.PromoPreview{Code: out.Code, DiscountCents: out.DiscountCents}, nil
}

func (c *Client) Redeem(ctx context.Context, code, orderID, customerID string, orderAmountCents int64) (int64, error) {
	body, err := json.Marshal(map[string]any{
		"code":               code,
		"order_id":           orderID,
		"customer_id":        customerID,
		"order_amount_cents": orderAmountCents,
	})
	if err != nil {
		return 0, err
	}
	var out promoResponse
	if err := c.do(ctx, http.MethodPost, "/internal/promos/redeem", serviceKeyHeader, c.serviceKey, body, &out); err != nil {
		return 0, err
	}
	return out.DiscountCents, nil
}

func (c *Client) Release(ctx context.Context, orderID string) error {
	return c.do(ctx, http.MethodDelete, "/internal/promos/redemptions/"+url.PathEscape(orderID), serviceKeyHeader, c.serviceKey, nil, nil)
}

func (c *Client) do(ctx context.Context, method, path, authHeader, authValue string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(authHeader, authValue)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("promo-service unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("promo-service returned %d", resp.StatusCode)
		}
		return fmt.Errorf("%s", e.Error)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("invalid promo-service response: %w", err)
	}
	return nil
}
