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
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"goodfood/order-service/internal/domain"
)

// maxLookupIDs is the number of menu item ids menu-service accepts per call.
const maxLookupIDs = 200

type Client struct {
	baseURL    string
	serviceKey string
	http       *http.Client
}

var (
	_ domain.RecipeService = (*Client)(nil)
	_ domain.MenuCatalog   = (*Client)(nil)
)

func New(baseURL, serviceKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: baseURL, serviceKey: serviceKey, http: httpClient}
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

// ── Price authority ─────────────────────────────────────────

// menuEntry is the part of a menu-service dish that decides whether and at
// what price it can be ordered.
type menuEntry struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PriceCents      int64  `json:"price_cents"`
	Available       bool   `json:"available"`
	HiddenForViewer bool   `json:"hidden_for_viewer"`
}

// PriceItems prices an order's lines from the restaurant's public menu — the
// same list its customers browse, which already includes head-office (global)
// dishes and leaves out those this restaurant has hidden.
func (c *Client) PriceItems(ctx context.Context, restaurantID string, items []domain.OrderItem) ([]domain.OrderItem, error) {
	menu, err := c.restaurantMenu(ctx, "/api/menu", restaurantID)
	if err != nil {
		return nil, err
	}
	// A line that is no dish may be a formule: those are only fetched then.
	for _, item := range items {
		if _, ok := menu[canonicalID(item.MenuItemID)]; ok {
			continue
		}
		formules, err := c.restaurantMenu(ctx, "/api/menu-plans", restaurantID)
		if err != nil {
			return nil, err
		}
		for id, f := range formules {
			menu[id] = f
		}
		break
	}

	priced := make([]domain.OrderItem, 0, len(items))
	for _, item := range items {
		dish, ok := menu[canonicalID(item.MenuItemID)]
		if !ok || !dish.Available || dish.HiddenForViewer {
			return nil, domain.NewValidationError(fmt.Sprintf("« %s » n'est pas disponible dans ce restaurant", item.MenuItemName))
		}
		if dish.PriceCents <= 0 {
			return nil, fmt.Errorf("menu-service returned a non-positive price for dish %s", dish.ID)
		}
		item.MenuItemName = dish.Name
		item.UnitPriceCents = dish.PriceCents
		priced = append(priced, item)
	}
	return priced, nil
}

// restaurantMenu reads the restaurant's public dishes (/api/menu) or
// formules (/api/menu-plans): both carry id, name, price and availability.
func (c *Client) restaurantMenu(ctx context.Context, path, restaurantID string) (map[string]menuEntry, error) {
	endpoint := c.baseURL + path + "?restaurantId=" + url.QueryEscape(restaurantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("menu-service request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("menu-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("menu-service returned %d", resp.StatusCode)
	}

	var entries []menuEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("invalid menu-service response: %w", err)
	}
	menu := make(map[string]menuEntry, len(entries))
	for _, e := range entries {
		menu[canonicalID(e.ID)] = e
	}
	return menu, nil
}

// canonicalID is the lowercase hyphenated form orders store (see NewOrder),
// so ids compare equal however menu-service happens to spell them.
func canonicalID(id string) string {
	if parsed, err := uuid.Parse(strings.TrimSpace(id)); err == nil {
		return parsed.String()
	}
	return strings.ToLower(strings.TrimSpace(id))
}
