package domain

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// StockSyncState tracks where an order stands with stock-service.
type StockSyncState string

const (
	StockSyncNone           StockSyncState = "NONE"
	StockSyncConsumePending StockSyncState = "CONSUME_PENDING"
	StockSyncConsumed       StockSyncState = "CONSUMED"
	StockSyncRestorePending StockSyncState = "RESTORE_PENDING"
	StockSyncRestored       StockSyncState = "RESTORED"
)

// StockLine is a quantity of one stock item (an ingredient).
type StockLine struct {
	StockItemID string
	Quantity    float64
}

// MissingStock describes an ingredient that cannot cover an order.
type MissingStock struct {
	StockItemID string
	Name        string
	Unit        string
	Required    float64
	OnHand      float64
}

// StockAvailability is the answer of stock-service to a check.
type StockAvailability struct {
	Available bool
	Missing   []MissingStock
}

// RecipeService is the outbound port to menu-service recipes. Dishes without
// a recipe are absent from the returned map.
type RecipeService interface {
	Lookup(ctx context.Context, restaurantID string, menuItemIDs []string) (map[string][]StockLine, error)
}

// StockService is the outbound port to stock-service.
type StockService interface {
	Check(ctx context.Context, restaurantID string, lines []StockLine) (*StockAvailability, error)
	Consume(ctx context.Context, orderID, restaurantID string, lines []StockLine) error
	Restore(ctx context.Context, orderID, restaurantID string) error
}

// round3 rounds to the 3 decimals of NUMERIC(12,3), absorbing float noise.
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// RequiredIngredients computes the ingredients an order needs: line quantity
// times recipe quantity, cumulated per stock item, sorted by StockItemID and
// rounded to 3 decimals. Dishes without a recipe are ignored.
func RequiredIngredients(items []OrderItem, recipes map[string][]StockLine) []StockLine {
	totals := map[string]float64{}
	for _, item := range items {
		for _, line := range recipes[item.MenuItemID] {
			totals[line.StockItemID] += float64(item.Quantity) * line.Quantity
		}
	}
	out := make([]StockLine, 0, len(totals))
	for id, qty := range totals {
		out = append(out, StockLine{StockItemID: id, Quantity: round3(qty)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StockItemID < out[j].StockItemID })
	return out
}

func formatQty(v float64) string { return strconv.FormatFloat(round3(v), 'f', -1, 64) }

// ShortageMessage builds the customer-facing message for the first missing
// ingredient, naming the first dish of the order whose recipe uses it.
func ShortageMessage(items []OrderItem, recipes map[string][]StockLine, missing []MissingStock) string {
	if len(missing) == 0 {
		return "Stock insuffisant"
	}
	m := missing[0]
	for _, item := range items {
		for _, line := range recipes[item.MenuItemID] {
			if line.StockItemID == m.StockItemID {
				return fmt.Sprintf("Stock insuffisant pour « %s » : %s (besoin %s %s, disponible %s)",
					item.MenuItemName, m.Name, formatQty(m.Required), m.Unit, formatQty(m.OnHand))
			}
		}
	}
	return fmt.Sprintf("Stock insuffisant : %s (besoin %s %s, disponible %s)",
		m.Name, formatQty(m.Required), m.Unit, formatQty(m.OnHand))
}
