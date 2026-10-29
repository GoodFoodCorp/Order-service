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

const (
	burgerID = "11111111-1111-4111-8111-111111111111"
	globalID = "33333333-3333-4333-8333-333333333333"
)

const menuJSON = `[
  {"id":"11111111-1111-4111-8111-111111111111","restaurant_id":"resto-1","name":"Burger Deluxe","price_cents":1299,"available":true,"is_global":false,"hidden_for_viewer":false},
  {"id":"22222222-2222-4222-8222-222222222222","restaurant_id":"resto-1","name":"Pizza épuisée","price_cents":1499,"available":false,"is_global":false,"hidden_for_viewer":false},
  {"id":"33333333-3333-4333-8333-333333333333","restaurant_id":"","name":"Coca (siège)","price_cents":250,"available":true,"is_global":true,"hidden_for_viewer":false},
  {"id":"44444444-4444-4444-8444-444444444444","restaurant_id":"","name":"Plat masqué","price_cents":900,"available":true,"is_global":true,"hidden_for_viewer":true}
]`

func serve(t *testing.T, status int, body string) (*Client, *http.Request) {
	t.Helper()
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(r.Context())
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "svc-key", nil), &got
}

func line(id, name string, qty int, price int64) domain.OrderItem {
	return domain.OrderItem{ID: "line-" + id[:2], MenuItemID: id, MenuItemName: name, Quantity: qty, UnitPriceCents: price}
}

func TestPriceItemsUsesTheCatalogPriceAndName(t *testing.T) {
	c, req := serve(t, 200, menuJSON)

	out, err := c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line(burgerID, "Gratuit", 2, 1)})

	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "Burger Deluxe", out[0].MenuItemName)
	assert.Equal(t, int64(1299), out[0].UnitPriceCents)
	assert.Equal(t, 2, out[0].Quantity, "quantity stays the customer's")
	assert.Equal(t, "line-11", out[0].ID, "everything else on the line is untouched")
	assert.Equal(t, "/api/menu", req.URL.Path)
	assert.Equal(t, "resto-1", req.URL.Query().Get("restaurantId"), "the order's own restaurant, never another's menu")
}

func TestPriceItemsAcceptsGlobalDishesOfHeadOffice(t *testing.T) {
	c, _ := serve(t, 200, menuJSON)

	out, err := c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line(globalID, "Coca", 1, 1)})

	require.NoError(t, err)
	assert.Equal(t, int64(250), out[0].UnitPriceCents)
}

func TestPriceItemsRefusesWhatCannotBeOrdered(t *testing.T) {
	for name, id := range map[string]string{
		"unknown dish":            "99999999-9999-4999-8999-999999999999",
		"unavailable dish":        "22222222-2222-4222-8222-222222222222",
		"dish hidden by this one": "44444444-4444-4444-8444-444444444444",
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := serve(t, 200, menuJSON)

			_, err := c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line(burgerID, "Burger", 1, 1299), line(id, "Plat", 1, 1)})

			var de *domain.Error
			require.ErrorAs(t, err, &de)
			assert.Equal(t, domain.ErrCodeValidation, de.Code)
			assert.Contains(t, de.Message, "Plat", "tells the customer which dish")
		})
	}
}

func TestPriceItemsRefusesAZeroPricedCatalogEntry(t *testing.T) {
	c, _ := serve(t, 200, `[{"id":"11111111-1111-4111-8111-111111111111","name":"Burger","price_cents":0,"available":true}]`)

	_, err := c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line(burgerID, "Burger", 1, 1299)})

	require.Error(t, err)
}

func TestPriceItemsMatchesIdsRegardlessOfCase(t *testing.T) {
	c, _ := serve(t, 200, `[{"id":"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA","name":"Burger","price_cents":1299,"available":true}]`)

	out, err := c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "Burger", 1, 1)})

	require.NoError(t, err)
	assert.Equal(t, int64(1299), out[0].UnitPriceCents)
}

func TestPriceItemsFailsWhenMenuServiceFails(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"server error": {500, `{"error":"boom"}`},
		"bad json":     {200, `not json`},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := serve(t, tc.status, tc.body)

			_, err := c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line(burgerID, "Burger", 1, 1299)})

			require.Error(t, err)
			var de *domain.Error
			assert.False(t, asDomainError(err, &de), "an infrastructure failure is not a customer-facing validation error")
		})
	}

	c := New("http://127.0.0.1:1", "k", nil)
	_, err := c.PriceItems(context.Background(), "resto-1", []domain.OrderItem{line(burgerID, "Burger", 1, 1299)})
	require.Error(t, err, "unreachable")
}

func asDomainError(err error, target **domain.Error) bool {
	for err != nil {
		if de, ok := err.(*domain.Error); ok {
			*target = de
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
