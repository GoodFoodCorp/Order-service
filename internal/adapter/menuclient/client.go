// Package menuclient talks to menu-service's internal recipe endpoint. The
// shared service key authenticates this service; it is never logged or
// included in errors.
package menuclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"goodfood/order-service/internal/domain"
)

// maxLookupIDs is the number of menu item ids menu-service accepts per call.
const maxLookupIDs = 200

type Client struct {
	baseURL    string
	serviceKey string
	http       *http.Client
}

var _ domain.RecipeService = (*Client)(nil)

func New(baseURL, serviceKey string) *Client {
	return &Client{baseURL: baseURL, serviceKey: serviceKey, http: &http.Client{Timeout: 10 * time.Second}}
}

type lookupRequest struct {
	RestaurantID string   `json:"restaurant_id"`
	MenuItemIDs  []string `json:"menu_item_ids"`
}

type lookupResponse struct {
	Recipes map[string][]struct {
		StockItemID string  `json:"stock_item_id"`
		Quantity    float64 `json:"quantity"`
	} `json:"recipes"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Lookup returns the recipes of the given dishes, keyed by menu item id.
// Dishes without a recipe are absent. Ids are de-duplicated and sent in
// chunks of at most 200.
func (c *Client) Lookup(ctx context.Context, restaurantID string, menuItemIDs []string) (map[string][]domain.StockLine, error) {
	seen := make(map[string]struct{}, len(menuItemIDs))
	ids := make([]string, 0, len(menuItemIDs))
	for _, id := range menuItemIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	out := map[string][]domain.StockLine{}
	// Always at least one call so the request stays valid (and checked) for
	// an empty list.
	for start := 0; start == 0 || start < len(ids); start += maxLookupIDs {
		end := min(start+maxLookupIDs, len(ids))
		if err := c.lookupChunk(ctx, restaurantID, ids[start:end], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *Client) lookupChunk(ctx context.Context, restaurantID string, ids []string, out map[string][]domain.StockLine) error {
	body, err := json.Marshal(lookupRequest{RestaurantID: restaurantID, MenuItemIDs: ids})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/recipes/lookup", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("menu-service request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", c.serviceKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("menu-service unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e errorResponse
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error != "" {
			return fmt.Errorf("menu-service returned %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("menu-service returned %d", resp.StatusCode)
	}

	var parsed lookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("invalid menu-service response: %w", err)
	}
	for menuItemID, lines := range parsed.Recipes {
		stock := make([]domain.StockLine, 0, len(lines))
		for _, l := range lines {
			stock = append(stock, domain.StockLine{StockItemID: l.StockItemID, Quantity: l.Quantity})
		}
		out[menuItemID] = stock
	}
	return nil
}
