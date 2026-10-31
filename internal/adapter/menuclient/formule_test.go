package menuclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goodfood/order-service/internal/domain"
)

const formuleID = "55555555-5555-4555-8555-555555555555"

const plansJSON = `[
  {"id":"55555555-5555-4555-8555-555555555555","restaurant_id":"resto-1","name":"Menu midi","price_cents":1690,"available":true,"is_global":false,"hidden_for_viewer":false,"dish_ids":["11111111-1111-4111-8111-111111111111"]}
]`

// A formule is priced like a dish, from the restaurant's own formules; they
// are only fetched when the order holds something that is not a dish.
func TestPriceItemsPricesFormules(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		assert.Equal(t, "resto-1", r.URL.Query().Get("restaurantId"))
		if r.URL.Path == "/api/menu-plans" {
			_, _ = w.Write([]byte(plansJSON))
			return
		}
		_, _ = w.Write([]byte(menuJSON))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "svc-key", nil)

	out, err := c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line(burgerID, "x", 1, 1), line(formuleID, "Gratuit", 2, 1)})
	require.NoError(t, err)
	assert.Equal(t, "Menu midi", out[1].MenuItemName)
	assert.Equal(t, int64(1690), out[1].UnitPriceCents)
	assert.Equal(t, []string{"/api/menu", "/api/menu-plans"}, paths)

	paths = nil
	_, err = c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line(burgerID, "x", 1, 1)})
	require.NoError(t, err)
	assert.Equal(t, []string{"/api/menu"}, paths, "dishes only: no formule lookup")

	_, err = c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line("66666666-6666-4666-8666-666666666666", "Inconnu", 1, 1)})
	assert.Error(t, err, "neither a dish nor a formule")
}
